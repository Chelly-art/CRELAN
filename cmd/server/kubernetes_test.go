package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestWorkspaceNamespaceRequiresCapsuleTenantAssignment(t *testing.T) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "crelan-ws-42",
		Labels: map[string]string{capsuleTenantLabel: "crelan-ws-42"},
	}}
	client := k8sfake.NewSimpleClientset(namespace)
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{
			namespacePrefix: "crelan-ws-",
			tenantPrefix:    "crelan-ws-",
		},
	}
	got, err := provisioner.workspaceNamespace(context.Background(), 42)
	if err != nil || got != namespace.Name {
		t.Fatalf("workspace namespace = %q, error = %v", got, err)
	}

	namespace.Labels[capsuleTenantLabel] = "another-workspace"
	if _, err := client.CoreV1().Namespaces().Update(context.Background(), namespace, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update namespace tenant label: %v", err)
	}
	if _, err := provisioner.workspaceNamespace(context.Background(), 42); err == nil {
		t.Fatal("expected namespace mapped to a different Capsule tenant to be rejected")
	}
}

func TestApplyApplicationCreatesWorkspaceScopedResources(t *testing.T) {
	client := k8sfake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "crelan-ws-42"}})
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{
			baseDomain:   "apps.example.test",
			ingressClass: "nginx",
			tlsSecret:    "crelan-apps-tls",
		},
	}
	if err := provisioner.applyApplication(context.Background(), 42, "crelan-ws-42", DeploymentRequest{
		Name: "storefront", Port: 3000, CPUMillicores: 1000, MemoryMB: 512, WorkspaceMaxApps: 5,
	}, "registry.example.test/apps/storefront@sha256:abc"); err != nil {
		t.Fatalf("apply app resources: %v", err)
	}

	deployment, err := client.AppsV1().Deployments("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.Image != "registry.example.test/apps/storefront@sha256:abc" ||
		container.Ports[0].ContainerPort != 3000 ||
		deployment.Labels[workspaceLabel] != "42" ||
		deployment.Spec.Template.Labels[applicationLabel] != "storefront" {
		t.Fatalf("unexpected deployment spec: %+v", deployment.Spec)
	}
	if container.SecurityContext == nil || container.SecurityContext.AllowPrivilegeEscalation == nil ||
		*container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("application container must disallow privilege escalation")
	}

	service, err := client.CoreV1().Services("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if service.Spec.Selector[workspaceLabel] != "42" || service.Spec.Ports[0].TargetPort.IntVal != 3000 {
		t.Fatalf("unexpected service spec: %+v", service.Spec)
	}

	ingress, err := client.NetworkingV1().Ingresses("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ingress: %v", err)
	}
	if ingress.Spec.Rules[0].Host != "storefront-ws-42.apps.example.test" ||
		ingress.Spec.TLS[0].SecretName != "crelan-apps-tls" ||
		*ingress.Spec.IngressClassName != "nginx" {
		t.Fatalf("unexpected ingress spec: %+v", ingress.Spec)
	}
}

func TestApplicationResourcesExposeWorkspaceMetadataWithoutSecretValues(t *testing.T) {
	namespace := "crelan-ws-42"
	client := k8sfake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: namespace, Labels: map[string]string{capsuleTenantLabel: namespace},
		}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "storefront-abc", Namespace: namespace,
			Labels: map[string]string{workspaceLabel: "42", applicationLabel: "storefront"},
		}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "storefront", Ready: true, RestartCount: 2},
		}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: namespace, Labels: map[string]string{workspaceLabel: "42", applicationLabel: "storefront"},
		}, Data: map[string]string{"MODE": "production"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "app-secret", Namespace: namespace, Labels: map[string]string{workspaceLabel: "42", applicationLabel: "storefront"},
		}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"API_KEY": []byte("must-not-leak")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "other-workspace-secret", Namespace: namespace, Labels: map[string]string{workspaceLabel: "7"},
		}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"API_KEY": []byte("other-tenant")}},
	)
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{namespacePrefix: "crelan-ws-", tenantPrefix: "crelan-ws-"},
	}
	resources, err := provisioner.ApplicationResources(context.Background(), 42, "storefront")
	if err != nil {
		t.Fatalf("load application resources: %v", err)
	}
	if len(resources.Pods) != 1 || !resources.Pods[0].Ready || resources.Pods[0].Restarts != 2 {
		t.Fatalf("unexpected app instances: %+v", resources.Pods)
	}
	if len(resources.ConfigMaps) != 1 || resources.ConfigMaps[0].Name != "app-config" ||
		len(resources.ConfigMaps[0].Keys) != 1 || resources.ConfigMaps[0].Keys[0] != "MODE" {
		t.Fatalf("unexpected workspace configuration: %+v", resources.ConfigMaps)
	}
	if len(resources.Secrets) != 1 || resources.Secrets[0].Name != "app-secret" ||
		len(resources.Secrets[0].Keys) != 1 || resources.Secrets[0].Keys[0] != "API_KEY" {
		t.Fatalf("unexpected safe secret metadata: %+v", resources.Secrets)
	}
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatalf("encode runtime resources: %v", err)
	}
	if strings.Contains(string(encoded), "must-not-leak") || strings.Contains(string(encoded), "other-tenant") ||
		strings.Contains(string(encoded), "production") {
		t.Fatalf("runtime response included resource values or another workspace data: %s", encoded)
	}
}

func TestConfigureApplicationWritesWorkspaceScopedEnvironmentAndSecrets(t *testing.T) {
	const namespace = "crelan-ws-42"
	labels := map[string]string{workspaceLabel: "42", applicationLabel: "storefront"}
	client := k8sfake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: namespace, Labels: map[string]string{capsuleTenantLabel: namespace},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: namespace, Labels: labels},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "storefront"}},
			}}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "another-env", Namespace: namespace,
			Labels: map[string]string{workspaceLabel: "42", applicationLabel: "another-app"}}, Data: map[string]string{"SHOULD_NOT_APPEAR": "no"}},
	)
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{namespacePrefix: "crelan-ws-", tenantPrefix: "crelan-ws-"},
	}
	if err := provisioner.ConfigureApplication(context.Background(), 42, "storefront",
		map[string]string{"MODE": "production"}, map[string]string{"API_KEY": "secret-value"}); err != nil {
		t.Fatalf("configure application: %v", err)
	}
	config, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), "storefront-env", metav1.GetOptions{})
	if err != nil || config.Data["MODE"] != "production" {
		t.Fatalf("unexpected environment ConfigMap: config=%+v err=%v", config, err)
	}
	secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), "storefront-secrets", metav1.GetOptions{})
	if err != nil || string(secret.Data["API_KEY"]) != "secret-value" {
		t.Fatalf("unexpected app Secret: secret=%+v err=%v", secret, err)
	}
	deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), "storefront", metav1.GetOptions{})
	if err != nil || len(deployment.Spec.Template.Spec.Containers[0].EnvFrom) != 2 {
		t.Fatalf("application rollout does not reference config resources: deployment=%+v err=%v", deployment, err)
	}
	resources, err := provisioner.ApplicationResources(context.Background(), 42, "storefront")
	if err != nil || len(resources.ConfigMaps) != 1 || len(resources.Secrets) != 1 {
		t.Fatalf("resources are not app-scoped: resources=%+v err=%v", resources, err)
	}
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatalf("encode app resources: %v", err)
	}
	if strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "production") ||
		strings.Contains(string(encoded), "SHOULD_NOT_APPEAR") {
		t.Fatalf("resource API exposed configuration values or another app: %s", encoded)
	}
}

func TestConfigureCustomDomainAddsOwnedIngressWithTLS(t *testing.T) {
	const namespace = "crelan-ws-42"
	labels := map[string]string{workspaceLabel: "42", applicationLabel: "storefront"}
	client := k8sfake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: namespace, Labels: map[string]string{capsuleTenantLabel: namespace},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: namespace, Labels: labels}},
	)
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{namespacePrefix: "crelan-ws-", tenantPrefix: "crelan-ws-", ingressClass: "nginx"},
	}
	if err := provisioner.ConfigureCustomDomain(context.Background(), 42, "storefront", "app.example.test", "letsencrypt"); err != nil {
		t.Fatalf("configure custom domain: %v", err)
	}
	ingressNameHash := sha256.Sum256([]byte("app.example.test"))
	ingressName := "crelan-domain-" + hex.EncodeToString(ingressNameHash[:6])
	ingress, err := client.NetworkingV1().Ingresses(namespace).Get(context.Background(), ingressName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("load custom-domain ingress: %v", err)
	}
	if ingress.Spec.Rules[0].Host != "app.example.test" ||
		ingress.Spec.TLS[0].Hosts[0] != "app.example.test" ||
		ingress.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt" ||
		ingress.Annotations["external-dns.alpha.kubernetes.io/hostname"] != "app.example.test" {
		t.Fatalf("unexpected custom-domain ingress: %+v", ingress)
	}
	if err := provisioner.DeleteCustomDomain(context.Background(), 42, "storefront", "app.example.test"); err != nil {
		t.Fatalf("delete custom-domain ingress: %v", err)
	}
	if _, err := client.NetworkingV1().Ingresses(namespace).Get(context.Background(), ingressName, metav1.GetOptions{}); err == nil {
		t.Fatal("custom-domain ingress remains after deletion")
	}
}

func TestDeleteRemovesOnlyWorkspaceApplication(t *testing.T) {
	labels := map[string]string{workspaceLabel: "42", applicationLabel: "storefront"}
	client := k8sfake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "crelan-ws-42",
			Labels: map[string]string{capsuleTenantLabel: "crelan-ws-42"},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "crelan-ws-42", Labels: labels}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "crelan-ws-42", Labels: labels}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "crelan-ws-42", Labels: labels}},
	)
	provisioner := &kubernetesProvisioner{
		client: client,
		config: kubernetesProvisionerConfig{namespacePrefix: "crelan-ws-", tenantPrefix: "crelan-ws-"},
	}
	if err := provisioner.Delete(context.Background(), 42, "storefront"); err != nil {
		t.Fatalf("delete app resources: %v", err)
	}
	if _, err := client.AppsV1().Deployments("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{}); err == nil {
		t.Fatal("deployment still exists after delete")
	}
	if _, err := client.CoreV1().Services("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{}); err == nil {
		t.Fatal("service still exists after delete")
	}
	if _, err := client.NetworkingV1().Ingresses("crelan-ws-42").Get(context.Background(), "storefront", metav1.GetOptions{}); err == nil {
		t.Fatal("ingress still exists after delete")
	}
}

func TestLoadKubernetesProvisionerConfig(t *testing.T) {
	t.Setenv("CRELAN_IMAGE_REGISTRY", "ghcr.io/acme/crelan")
	t.Setenv("CRELAN_REGISTRY_SECRET", "registry-auth")
	t.Setenv("CRELAN_BUILD_TIMEOUT", "8m")
	t.Setenv("CRELAN_WORKSPACE_NAMESPACE_PREFIX", "crelan-ws-")
	t.Setenv("CRELAN_CAPSULE_TENANT_PREFIX", "crelan-ws-")
	t.Setenv("CRELAN_BUILD_SERVICE_ACCOUNT", "crelan-build")
	t.Setenv("CRELAN_KPACK_BUILDER_IMAGE", "paketobuildpacks/builder-jammy-base:latest")
	t.Setenv("CRELAN_BUILDKIT_IMAGE", "moby/buildkit:v0.20.2-rootless")
	t.Setenv("CRELAN_GIT_IMAGE", "alpine/git:v2.47.2")
	t.Setenv("CRELAN_APP_BASE_DOMAIN", "apps.example.test")
	t.Setenv("CRELAN_INGRESS_CLASS", "nginx")
	t.Setenv("CRELAN_INGRESS_TLS_SECRET", "crelan-tls")

	config, err := loadKubernetesProvisionerConfig()
	if err != nil {
		t.Fatalf("load provisioner config: %v", err)
	}
	if config.imageRegistry != "ghcr.io/acme/crelan" ||
		config.registrySecret != "registry-auth" ||
		config.buildTimeout.String() != "8m0s" ||
		config.baseDomain != "apps.example.test" ||
		config.kpackBuilder == "" {
		t.Fatalf("unexpected provisioner config: %+v", config)
	}
}

func TestLoadKubernetesProvisionerConfigRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("CRELAN_BUILD_TIMEOUT", "10s")
	if _, err := loadKubernetesProvisionerConfig(); err == nil {
		t.Fatal("expected invalid build timeout to be rejected")
	}
}

func TestKpackBuildUsesServiceAccountNameAndReportsImage(t *testing.T) {
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var created *unstructured.Unstructured
	dynamicClient.PrependReactor("create", "builds", func(action k8stesting.Action) (bool, runtime.Object, error) {
		created = action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		return false, nil, nil
	})
	dynamicClient.PrependReactor("get", "builds", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "kpack.io/v1alpha2",
			"kind":       "Build",
			"metadata":   map[string]any{"name": "storefront-build"},
			"status": map[string]any{
				"latestImage": "registry.example.test/storefront@sha256:abc",
				"conditions": []any{map[string]any{
					"type":   "Succeeded",
					"status": "True",
				}},
			},
		}}, nil
	})
	provisioner := &kubernetesProvisioner{
		dynamicClient: dynamicClient,
		config: kubernetesProvisionerConfig{
			buildService: "crelan-build",
			buildTimeout: time.Minute,
		},
	}
	image, err := provisioner.buildWithKpack(context.Background(), "crelan-ws-42", DeploymentRequest{
		Name:          "storefront",
		RepositoryURL: "https://github.com/acme/storefront",
		Branch:        "main",
	}, "storefront-build", "registry.example.test/storefront:build")
	if err != nil {
		t.Fatalf("run kpack build: %v", err)
	}
	serviceAccount, found, err := unstructured.NestedString(created.Object, "spec", "serviceAccountName")
	if err != nil || !found || serviceAccount != "crelan-build" {
		t.Fatalf("kpack service account = %q, found=%v, error=%v", serviceAccount, found, err)
	}
	if image != "registry.example.test/storefront@sha256:abc" {
		t.Fatalf("kpack image = %q", image)
	}
}

func TestBuildResourceNameFitsDNSLabel(t *testing.T) {
	name, err := buildResourceName("a-very-long-application-name-that-is-close-to-the-maximum-length")
	if err != nil {
		t.Fatalf("create build name: %v", err)
	}
	if len(name) > 63 || !deploymentNamePattern.MatchString(name) {
		t.Fatalf("invalid Kubernetes build name %q", name)
	}
}

func TestGitCloneArgsKeepRepositoryAndBranchAsArguments(t *testing.T) {
	args := gitCloneArgs(DeploymentRequest{
		RepositoryURL: "https://github.com/acme/example",
		Branch:        "release/v1",
	})
	expected := []string{"clone", "--depth", "1", "--branch", "release/v1", "--", "https://github.com/acme/example", "/workspace"}
	if len(args) != len(expected) {
		t.Fatalf("git args = %q", args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Fatalf("git args = %q, want %q", args, expected)
		}
	}
}

func TestBuildResourceNameIsUnique(t *testing.T) {
	first, err := buildResourceName("app")
	if err != nil {
		t.Fatalf("first name: %v", err)
	}
	second, err := buildResourceName("app")
	if err != nil {
		t.Fatalf("second name: %v", err)
	}
	if first == second {
		t.Fatalf("build names should be unique: %q", first)
	}
}
