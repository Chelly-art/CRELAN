package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	capsuleTenantLabel = "capsule.clastix.io/tenant"
	workspaceLabel     = "crelan.io/workspace-id"
	applicationLabel   = "crelan.io/application"
	kpackBuildResource = "builds"
)

var (
	errKubernetesNotConfigured = errors.New("Kubernetes client is not configured")
	errBuildProviderNotReady   = errors.New("build provider is not configured")
	errBuildFailed             = errors.New("application build failed")
	errPrivateBuildpacks       = errors.New("private GitHub repositories without a Dockerfile require BuildKit")
	kpackBuildGVR              = k8sschema.GroupVersionResource{
		Group: "kpack.io", Version: "v1alpha2", Resource: kpackBuildResource,
	}
	buildkitDigestPattern = regexp.MustCompile(`"containerimage\.digest"\s*:\s*"(sha256:[a-f0-9]{64})"`)
)

type kubernetesProvisioner struct {
	client        kubernetes.Interface
	dynamicClient dynamic.Interface
	httpClient    *http.Client
	config        kubernetesProvisionerConfig
}

func gitCloneCommand() string {
	return `if [ -n "$GITHUB_TOKEN" ]; then
  export GIT_CONFIG_COUNT=1
  export GIT_CONFIG_KEY_0=http.https://github.com/.extraheader
  export GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $(printf 'x-access-token:%s' "$GITHUB_TOKEN" | base64 | tr -d '\n')"
fi
if [ -n "$CRELAN_BRANCH" ]; then
  git clone --depth 1 --branch "$CRELAN_BRANCH" -- "$CRELAN_REPOSITORY" /workspace
else
  git clone --depth 1 -- "$CRELAN_REPOSITORY" /workspace
fi`
}

type kubernetesProvisionerConfig struct {
	namespacePrefix   string
	tenantPrefix      string
	imageRegistry     string
	registrySecret    string
	buildService      string
	kpackBuilder      string
	buildkitImage     string
	gitImage          string
	baseDomain        string
	ingressClass      string
	tlsSecret         string
	certManagerIssuer string
	buildTimeout      time.Duration
}

func newKubernetesProvisioner() (*kubernetesProvisioner, error) {
	config, err := loadKubernetesRESTConfig()
	if err != nil {
		return nil, err
	}
	return newKubernetesProvisionerWithRESTConfig(config)
}

func newKubernetesProvisionerForContext(contextName string) (*kubernetesProvisioner, error) {
	loaded, err := loadConfiguredKubeconfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	if loaded == nil || loaded.Contexts[contextName] == nil {
		return nil, fmt.Errorf("kubeconfig context %q was not found", contextName)
	}
	config, err := clientcmd.NewNonInteractiveClientConfig(*loaded, contextName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig context %q: %w", contextName, err)
	}
	return newKubernetesProvisionerWithRESTConfig(config)
}

func loadKubeconfigContexts() ([]string, string, error) {
	loaded, err := loadConfiguredKubeconfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}
	if loaded == nil || len(loaded.Contexts) == 0 {
		return nil, "", errKubernetesNotConfigured
	}
	contexts := make([]string, 0, len(loaded.Contexts))
	for name := range loaded.Contexts {
		contexts = append(contexts, name)
	}
	sort.Strings(contexts)
	return contexts, loaded.CurrentContext, nil
}

func newKubernetesProvisionerWithRESTConfig(config *rest.Config) (*kubernetesProvisioner, error) {
	config = rest.CopyConfig(config)
	if config.Timeout == 0 {
		config.Timeout = 15 * time.Second
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes dynamic client: %w", err)
	}

	provisionerConfig, err := loadKubernetesProvisionerConfig()
	if err != nil {
		return nil, err
	}
	return &kubernetesProvisioner{
		client:        client,
		dynamicClient: dynamicClient,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		config:        provisionerConfig,
	}, nil
}

func loadKubernetesRESTConfig() (*rest.Config, error) {
	if hasOperatorKubeconfigData() {
		loaded, err := loadConfiguredKubeconfig()
		if err != nil {
			return nil, fmt.Errorf("load operator kubeconfig: %w", err)
		}
		if loaded.CurrentContext == "" {
			return nil, errors.New("CRELAN_KUBECONFIG_DATA must set a current-context")
		}
		config, err := clientcmd.NewNonInteractiveClientConfig(*loaded, loaded.CurrentContext, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig context %q: %w", loaded.CurrentContext, err)
		}
		return config, nil
	}

	if kubeconfig := strings.TrimSpace(getenv("KUBECONFIG")); kubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load KUBECONFIG: %w", err)
		}
		return config, nil
	}

	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	loaded, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load default kubeconfig: %w", err)
	}
	if loaded == nil || len(loaded.Clusters) == 0 {
		return nil, errKubernetesNotConfigured
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load default kubeconfig: %w", err)
	}
	return config, nil
}

// loadConfiguredKubeconfig loads operator-provided kubeconfig YAML when set,
// otherwise it uses client-go's normal KUBECONFIG/default-path resolution.
func loadConfiguredKubeconfig() (*clientcmdapi.Config, error) {
	if data, err := operatorKubeconfigData(); err != nil {
		return nil, err
	} else if data != nil {
		loaded, err := clientcmd.Load(data)
		if err != nil {
			return nil, fmt.Errorf("parse operator kubeconfig data: %w", err)
		}
		return loaded, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	loaded, err := rules.Load()
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func hasOperatorKubeconfigData() bool {
	return strings.TrimSpace(getenv("CRELAN_KUBECONFIG_DATA_B64")) != "" ||
		strings.TrimSpace(getenv("CRELAN_KUBECONFIG_DATA")) != ""
}

func operatorKubeconfigData() ([]byte, error) {
	if encoded := strings.TrimSpace(getenv("CRELAN_KUBECONFIG_DATA_B64")); encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode CRELAN_KUBECONFIG_DATA_B64: %w", err)
		}
		return data, nil
	}
	if data := strings.TrimSpace(getenv("CRELAN_KUBECONFIG_DATA")); data != "" {
		return []byte(data), nil
	}
	return nil, nil
}

func loadKubernetesProvisionerConfig() (kubernetesProvisionerConfig, error) {
	registry := strings.TrimSuffix(strings.TrimSpace(getenv("CRELAN_IMAGE_REGISTRY")), "/")
	if registry != "" {
		parsed, err := url.Parse("https://" + registry)
		if err != nil || parsed.Host == "" || parsed.Path == "/" || parsed.RawQuery != "" ||
			parsed.Fragment != "" || parsed.User != nil {
			return kubernetesProvisionerConfig{}, errors.New("CRELAN_IMAGE_REGISTRY must be a registry host with an optional repository path")
		}
	}

	timeout := 30 * time.Minute
	if raw := strings.TrimSpace(getenv("CRELAN_BUILD_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < time.Minute || parsed > 2*time.Hour {
			return kubernetesProvisionerConfig{}, errors.New("CRELAN_BUILD_TIMEOUT must be between 1m and 2h")
		}
		timeout = parsed
	}

	return kubernetesProvisionerConfig{
		namespacePrefix:   envOrDefault("CRELAN_WORKSPACE_NAMESPACE_PREFIX", "crelan-ws-"),
		tenantPrefix:      envOrDefault("CRELAN_CAPSULE_TENANT_PREFIX", "crelan-ws-"),
		imageRegistry:     registry,
		registrySecret:    strings.TrimSpace(getenv("CRELAN_REGISTRY_SECRET")),
		buildService:      envOrDefault("CRELAN_BUILD_SERVICE_ACCOUNT", "crelan-build"),
		kpackBuilder:      strings.TrimSpace(getenv("CRELAN_KPACK_BUILDER_IMAGE")),
		buildkitImage:     envOrDefault("CRELAN_BUILDKIT_IMAGE", "moby/buildkit:v0.20.2-rootless"),
		gitImage:          envOrDefault("CRELAN_GIT_IMAGE", "alpine/git:v2.47.2"),
		baseDomain:        strings.Trim(strings.TrimSpace(getenv("CRELAN_APP_BASE_DOMAIN")), "."),
		ingressClass:      strings.TrimSpace(getenv("CRELAN_INGRESS_CLASS")),
		tlsSecret:         strings.TrimSpace(getenv("CRELAN_INGRESS_TLS_SECRET")),
		certManagerIssuer: strings.TrimSpace(getenv("CRELAN_CERT_MANAGER_ISSUER")),
		buildTimeout:      timeout,
	}, nil
}

func (p *kubernetesProvisioner) Deploy(ctx context.Context, workspaceID int64, request DeploymentRequest) (ProvisionedDeployment, error) {
	return p.DeployWithProgress(ctx, workspaceID, request, nil)
}

func (p *kubernetesProvisioner) DeployWithProgress(ctx context.Context, workspaceID int64, request DeploymentRequest, progress func(string) error) (ProvisionedDeployment, error) {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return ProvisionedDeployment{}, err
	}
	if progress != nil {
		if err := progress("building"); err != nil {
			return ProvisionedDeployment{}, err
		}
	}
	buildCtx, cancel := context.WithTimeout(ctx, p.config.buildTimeout)
	defer cancel()
	image, err := p.buildImage(buildCtx, workspaceID, namespace, request)
	if err != nil {
		return ProvisionedDeployment{}, err
	}
	if progress != nil {
		if err := progress("deploying"); err != nil {
			return ProvisionedDeployment{}, err
		}
	}
	if err := p.applyApplication(buildCtx, workspaceID, namespace, request, image); err != nil {
		return ProvisionedDeployment{}, fmt.Errorf("apply application resources: %w", err)
	}

	appURL := p.applicationURL(workspaceID, request.Name)
	return ProvisionedDeployment{Status: "deploying", URL: appURL}, nil
}

func (p *kubernetesProvisioner) WaitForWorkspaceApplication(ctx context.Context, workspaceID int64, name string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	return p.waitForApplication(ctx, namespace, name)
}

func (p *kubernetesProvisioner) CheckWorkspaceApplication(ctx context.Context, workspaceID int64, name string) (bool, error) {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	current, err := p.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read application rollout: %w", err)
	}
	return current.Status.AvailableReplicas > 0, nil
}

func (p *kubernetesProvisioner) ApplicationResources(ctx context.Context, workspaceID int64, name string) (applicationResources, error) {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return applicationResources{}, err
	}
	selector := labelsForApplication(workspaceID, name)
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return applicationResources{}, fmt.Errorf("list application pods: %w", err)
	}
	configMaps, err := p.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return applicationResources{}, fmt.Errorf("list workspace ConfigMaps: %w", err)
	}
	secrets, err := p.client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return applicationResources{}, fmt.Errorf("list workspace Secret metadata: %w", err)
	}
	result := applicationResources{
		Pods:       make([]podResource, 0, len(pods.Items)),
		ConfigMaps: make([]configMapResource, 0, len(configMaps.Items)),
		Secrets:    make([]secretResource, 0, len(secrets.Items)),
	}
	for _, pod := range pods.Items {
		ready := len(pod.Status.ContainerStatuses) > 0
		var restarts int32
		for _, status := range pod.Status.ContainerStatuses {
			ready = ready && status.Ready
			restarts += status.RestartCount
		}
		result.Pods = append(result.Pods, podResource{
			Name: pod.Name, Phase: string(pod.Status.Phase), Ready: ready, Restarts: restarts,
			CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	for _, configMap := range configMaps.Items {
		if !ownsWorkspace(configMap.Labels, workspaceID) || configMap.Labels[applicationLabel] != name {
			continue
		}
		keys := make([]string, 0, len(configMap.Data)+len(configMap.BinaryData))
		for key := range configMap.Data {
			keys = append(keys, key)
		}
		for key := range configMap.BinaryData {
			keys = append(keys, key)
		}
		result.ConfigMaps = append(result.ConfigMaps, configMapResource{
			Name: configMap.Name, Keys: keys, CreatedAt: configMap.CreationTimestamp.Time,
		})
	}
	for _, secret := range secrets.Items {
		if !ownsWorkspace(secret.Labels, workspaceID) || secret.Labels[applicationLabel] != name ||
			strings.HasSuffix(secret.Name, "-gh-auth") {
			continue
		}
		keys := make([]string, 0, len(secret.Data))
		for key := range secret.Data {
			keys = append(keys, key)
		}
		result.Secrets = append(result.Secrets, secretResource{
			Name: secret.Name, Type: string(secret.Type), Keys: keys, CreatedAt: secret.CreationTimestamp.Time,
		})
	}
	return result, nil
}

func (p *kubernetesProvisioner) WorkspaceResources(ctx context.Context, workspaceID int64) (clusterResources, error) {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return clusterResources{}, err
	}
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterResources{}, fmt.Errorf("list workspace pods: %w", err)
	}
	configMaps, err := p.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterResources{}, fmt.Errorf("list workspace ConfigMaps: %w", err)
	}
	secrets, err := p.client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return clusterResources{}, fmt.Errorf("list workspace Secret metadata: %w", err)
	}
	result := clusterResources{
		Namespace:  namespace,
		Pods:       make([]podResource, 0, len(pods.Items)),
		ConfigMaps: make([]configMapResource, 0, len(configMaps.Items)),
		Secrets:    make([]secretResource, 0, len(secrets.Items)),
	}
	for _, pod := range pods.Items {
		ready := len(pod.Status.ContainerStatuses) > 0
		var restarts int32
		for _, status := range pod.Status.ContainerStatuses {
			ready = ready && status.Ready
			restarts += status.RestartCount
		}
		result.Pods = append(result.Pods, podResource{
			Name: pod.Name, Phase: string(pod.Status.Phase), Ready: ready,
			Restarts: restarts, CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	for _, configMap := range configMaps.Items {
		keys := make([]string, 0, len(configMap.Data)+len(configMap.BinaryData))
		for key := range configMap.Data {
			keys = append(keys, key)
		}
		for key := range configMap.BinaryData {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result.ConfigMaps = append(result.ConfigMaps, configMapResource{
			Name: configMap.Name, Keys: keys, CreatedAt: configMap.CreationTimestamp.Time,
		})
	}
	for _, secret := range secrets.Items {
		keys := make([]string, 0, len(secret.Data))
		for key := range secret.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result.Secrets = append(result.Secrets, secretResource{
			Name: secret.Name, Type: string(secret.Type), Keys: keys, CreatedAt: secret.CreationTimestamp.Time,
		})
	}
	return result, nil
}

func (p *kubernetesProvisioner) ConfigureApplication(ctx context.Context, workspaceID int64, name string, environment, secretValues map[string]string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	deployments := p.client.AppsV1().Deployments(namespace)
	app, err := deployments.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("load application deployment: %w", err)
	}
	if !ownsWorkspace(app.Labels, workspaceID) || app.Labels[applicationLabel] != name {
		return errors.New("application is not owned by this workspace")
	}

	labels := map[string]string{workspaceLabel: strconv.FormatInt(workspaceID, 10), applicationLabel: name}
	configName := applicationResourceName(name, "env")
	configMaps := p.client.CoreV1().ConfigMaps(namespace)
	config, err := configMaps.Get(ctx, configName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		config = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configName, Namespace: namespace, Labels: labels}, Data: map[string]string{}}
	} else if err != nil {
		return fmt.Errorf("load application environment: %w", err)
	}
	if !ownsWorkspace(config.Labels, workspaceID) || config.Labels[applicationLabel] != name {
		return errors.New("application environment is not owned by this workspace")
	}
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	for key, value := range environment {
		config.Data[key] = value
	}
	config.Labels = labels
	if config.ResourceVersion == "" {
		_, err = configMaps.Create(ctx, config, metav1.CreateOptions{})
	} else {
		_, err = configMaps.Update(ctx, config, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("save application environment: %w", err)
	}

	secretName := applicationResourceName(name, "secrets")
	secrets := p.client.CoreV1().Secrets(namespace)
	secret, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace, Labels: labels}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{}}
	} else if err != nil {
		return fmt.Errorf("load application secrets: %w", err)
	}
	if !ownsWorkspace(secret.Labels, workspaceID) || secret.Labels[applicationLabel] != name {
		return errors.New("application secrets are not owned by this workspace")
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	for key, value := range secretValues {
		secret.Data[key] = []byte(value)
	}
	secret.Labels = labels
	if secret.ResourceVersion == "" {
		_, err = secrets.Create(ctx, secret, metav1.CreateOptions{})
	} else {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("save application secrets: %w", err)
	}

	app = app.DeepCopy()
	if len(app.Spec.Template.Spec.Containers) == 0 {
		return errors.New("application deployment has no container")
	}
	app.Spec.Template.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{
		{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: configName}, Optional: ptr(true)}},
		{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Optional: ptr(true)}},
	}
	if app.Spec.Template.Annotations == nil {
		app.Spec.Template.Annotations = map[string]string{}
	}
	app.Spec.Template.Annotations["crelan.io/config-updated-at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := deployments.Update(ctx, app, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("roll out application configuration: %w", err)
	}
	return nil
}

func applicationResourceName(name, suffix string) string {
	value := name + "-" + suffix
	if len(value) <= 63 {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return value[:54] + "-" + hex.EncodeToString(digest[:4])
}

func (p *kubernetesProvisioner) ApplicationLogs(ctx context.Context, workspaceID int64, name string, tail int) (string, error) {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelsForApplication(workspaceID, name),
	})
	if err != nil {
		return "", fmt.Errorf("list application pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", errors.New("application has no pods")
	}
	request := p.client.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		Container: name,
		TailLines: ptr(int64(tail)),
	})
	stream, err := request.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("open application logs: %w", err)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read application logs: %w", err)
	}
	return string(data), nil
}

func labelsForApplication(workspaceID int64, name string) string {
	return workspaceLabel + "=" + strconv.FormatInt(workspaceID, 10) + "," + applicationLabel + "=" + name
}

func ownsWorkspace(labels map[string]string, workspaceID int64) bool {
	return labels[workspaceLabel] == strconv.FormatInt(workspaceID, 10)
}

func (p *kubernetesProvisioner) Delete(ctx context.Context, workspaceID int64, name string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	propagation := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &propagation}
	var failures []error
	if err := p.client.AppsV1().Deployments(namespace).Delete(ctx, name, deleteOptions); err != nil && !apierrors.IsNotFound(err) {
		failures = append(failures, fmt.Errorf("delete deployment: %w", err))
	}
	if err := p.client.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		failures = append(failures, fmt.Errorf("delete service: %w", err))
	}
	if err := p.client.NetworkingV1().Ingresses(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		failures = append(failures, fmt.Errorf("delete ingress: %w", err))
	}
	return errors.Join(failures...)
}

func (p *kubernetesProvisioner) workspaceNamespace(ctx context.Context, workspaceID int64) (string, error) {
	if workspaceID < 1 {
		return "", errors.New("invalid workspace identifier")
	}
	namespace := p.config.namespacePrefix + strconv.FormatInt(workspaceID, 10)
	tenant := p.config.tenantPrefix + strconv.FormatInt(workspaceID, 10)
	if len(namespace) > 63 || len(tenant) > 63 || !deploymentNamePattern.MatchString(namespace) ||
		!deploymentNamePattern.MatchString(tenant) {
		return "", errors.New("workspace namespace or Capsule tenant name is not a valid Kubernetes name")
	}
	space, err := p.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("workspace namespace %q has not been provisioned", namespace)
		}
		return "", fmt.Errorf("get workspace namespace: %w", err)
	}
	if space.Labels[capsuleTenantLabel] != tenant {
		return "", fmt.Errorf("workspace namespace %q is not assigned to its expected Capsule tenant", namespace)
	}
	return namespace, nil
}

func (p *kubernetesProvisioner) buildImage(ctx context.Context, workspaceID int64, namespace string, request DeploymentRequest) (string, error) {
	if p.config.imageRegistry == "" || p.config.registrySecret == "" {
		return "", errBuildProviderNotReady
	}
	buildName, err := buildResourceName(request.Name)
	if err != nil {
		return "", err
	}
	tag := fmt.Sprintf("%s/workspace-%d/%s:%s", p.config.imageRegistry, workspaceID, request.Name, buildName)
	request.PrivateRepo, err = p.repositoryIsPrivate(ctx, request)
	if err != nil {
		return "", fmt.Errorf("check GitHub repository access: %w", err)
	}
	hasDockerfile, err := p.repositoryHasDockerfile(ctx, request)
	if err != nil {
		return "", fmt.Errorf("check repository Dockerfile: %w", err)
	}
	if hasDockerfile {
		return p.buildWithBuildKit(ctx, workspaceID, namespace, request, buildName, tag)
	}
	if request.PrivateRepo {
		return "", errPrivateBuildpacks
	}
	if p.config.kpackBuilder == "" {
		return "", errBuildProviderNotReady
	}
	return p.buildWithKpack(ctx, namespace, request, buildName, tag)
}

func (p *kubernetesProvisioner) repositoryIsPrivate(ctx context.Context, request DeploymentRequest) (bool, error) {
	parsed, err := url.Parse(request.RepositoryURL)
	if err != nil {
		return false, err
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/"), "/")
	if len(parts) != 2 {
		return false, errors.New("invalid GitHub repository URL")
	}
	endpoint := "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Crelan")
	if request.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+request.GitHubToken)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound && request.GitHubToken != "" {
		resp.Body.Close()
		publicRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			return false, requestErr
		}
		publicRequest.Header.Set("Accept", "application/vnd.github+json")
		publicRequest.Header.Set("User-Agent", "Crelan")
		resp, err = p.httpClient.Do(publicRequest)
		if err != nil {
			return false, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, errors.New("repository is not public or is not available to the connected GitHub App")
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return false, fmt.Errorf("GitHub repository API returned %s", resp.Status)
	}
	var repository struct {
		Private bool `json:"private"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&repository); err != nil {
		return false, fmt.Errorf("decode repository visibility: %w", err)
	}
	return repository.Private, nil
}

func (p *kubernetesProvisioner) repositoryHasDockerfile(ctx context.Context, request DeploymentRequest) (bool, error) {
	parsed, err := url.Parse(request.RepositoryURL)
	if err != nil {
		return false, err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return false, errors.New("invalid GitHub repository URL")
	}
	apiURL := &url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   "/repos/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git") + "/contents/" + request.Dockerfile,
	}
	query := apiURL.Query()
	if request.Branch != "" {
		query.Set("ref", request.Branch)
	}
	apiURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Crelan")
	if request.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+request.GitHubToken)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound && request.GitHubToken != "" {
		resp.Body.Close()
		publicRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
		if requestErr != nil {
			return false, requestErr
		}
		publicRequest.Header.Set("Accept", "application/vnd.github+json")
		publicRequest.Header.Set("User-Agent", "Crelan")
		resp, err = p.httpClient.Do(publicRequest)
		if err != nil {
			return false, err
		}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GitHub contents API returned %s", resp.Status)
	}
}

func (p *kubernetesProvisioner) buildWithKpack(ctx context.Context, namespace string, request DeploymentRequest, buildName, imageTag string) (string, error) {
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kpack.io/v1alpha2",
		"kind":       "Build",
		"metadata": map[string]any{
			"name":      buildName,
			"namespace": namespace,
			"labels": map[string]any{
				workspaceLabel:   namespaceWorkspaceID(namespace, p.config.namespacePrefix),
				applicationLabel: request.Name,
			},
		},
		"spec": map[string]any{
			"tags":                  []any{imageTag},
			"serviceAccountName":    p.config.buildService,
			"builder":               map[string]any{"image": p.config.kpackBuilder},
			"source":                map[string]any{"git": kpackGitSource(request)},
			"activeDeadlineSeconds": int64(p.config.buildTimeout.Seconds()),
			"cache": map[string]any{
				"registry": map[string]any{"tag": strings.TrimSuffix(imageTag, ":"+buildName) + ":cache"},
			},
		},
	}}
	build, err := p.dynamicClient.Resource(kpackBuildGVR).Namespace(namespace).Create(ctx, resource, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create kpack build: %w", err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		latest, err := p.dynamicClient.Resource(kpackBuildGVR).Namespace(namespace).Get(ctx, build.GetName(), metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("read kpack build status: %w", err)
		}
		status, _, _ := unstructured.NestedMap(latest.Object, "status")
		image, imageFound, _ := unstructured.NestedString(status, "latestImage")
		conditions, _, _ := unstructured.NestedSlice(status, "conditions")
		succeeded := false
		for _, condition := range conditions {
			entry, ok := condition.(map[string]any)
			if !ok || entry["type"] != "Succeeded" {
				continue
			}
			switch entry["status"] {
			case "True":
				succeeded = true
			case "False":
				return "", fmt.Errorf("%w: %s", errBuildFailed, conditionMessage(entry))
			}
		}
		if succeeded && imageFound && image != "" {
			return image, nil
		}
		if succeeded {
			return "", fmt.Errorf("%w: kpack reported success without an image reference", errBuildFailed)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for kpack build: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *kubernetesProvisioner) buildWithBuildKit(ctx context.Context, workspaceID int64, namespace string, request DeploymentRequest, buildName, imageTag string) (string, error) {
	if p.config.registrySecret == "" {
		return "", errBuildProviderNotReady
	}
	backoffLimit := int32(1)
	deadline := int64(p.config.buildTimeout.Seconds())
	gitAuthSecret := ""
	if request.GitHubToken != "" {
		gitAuthSecret = strings.TrimSuffix(buildName, "-build") + "-gh-auth"
		if len(gitAuthSecret) > 63 {
			gitAuthSecret = gitAuthSecret[:63]
		}
		_, err := p.client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: gitAuthSecret, Labels: map[string]string{
				workspaceLabel:   strconv.FormatInt(workspaceID, 10),
				applicationLabel: request.Name,
			}},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"token": []byte(request.GitHubToken)},
		}, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("create temporary GitHub source credential: %w", err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := p.client.CoreV1().Secrets(namespace).Delete(cleanupCtx, gitAuthSecret, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				log.Printf("remove temporary GitHub source credential %q: %v", gitAuthSecret, err)
			}
		}()
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildName,
			Namespace: namespace,
			Labels: map[string]string{
				workspaceLabel:   strconv.FormatInt(workspaceID, 10),
				applicationLabel: request.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: &deadline,
			TTLSecondsAfterFinished: func() *int32 {
				value := int32(86400)
				return &value
			}(),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					workspaceLabel:   strconv.FormatInt(workspaceID, 10),
					applicationLabel: request.Name,
				}},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: p.config.buildService,
					ImagePullSecrets:   []corev1.LocalObjectReference{{Name: p.config.registrySecret}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true),
						RunAsUser:    ptr(int64(1000)),
						RunAsGroup:   ptr(int64(1000)),
						FSGroup:      ptr(int64(1000)),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeUnconfined,
						},
					},
					InitContainers: []corev1.Container{{
						Name:    "checkout",
						Image:   p.config.gitImage,
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{gitCloneCommand()},
						Env: []corev1.EnvVar{
							{Name: "CRELAN_REPOSITORY", Value: request.RepositoryURL},
							{Name: "CRELAN_BRANCH", Value: request.Branch},
							{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
							{
								Name: "GITHUB_TOKEN",
								ValueFrom: func() *corev1.EnvVarSource {
									if gitAuthSecret == "" {
										return nil
									}
									return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{Name: gitAuthSecret},
										Key:                  "token",
									}}
								}(),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							RunAsNonRoot:             ptr(true),
							RunAsUser:                ptr(int64(1000)),
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "source", MountPath: "/workspace"}},
					}},
					Containers: []corev1.Container{{
						Name:    "build",
						Image:   p.config.buildkitImage,
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{buildkitBuildCommand()},
						Env: []corev1.EnvVar{
							{Name: "DOCKER_CONFIG", Value: "/home/user/.docker"},
							{Name: "BUILDKITD_FLAGS", Value: "--oci-worker-no-process-sandbox"},
							{Name: "CRELAN_DOCKERFILE", Value: request.Dockerfile},
							{Name: "CRELAN_IMAGE_TAG", Value: imageTag},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resourceMustParse("250m"),
								corev1.ResourceMemory: resourceMustParse("512Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resourceMustParse("1"),
								corev1.ResourceMemory: resourceMustParse("2Gi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							RunAsNonRoot:             ptr(true),
							RunAsUser:                ptr(int64(1000)),
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "source", MountPath: "/workspace"},
							{Name: "registry-auth", MountPath: "/home/user/.docker", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "source", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "registry-auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: p.config.registrySecret,
							Items: []corev1.KeyToPath{{
								Key:  ".dockerconfigjson",
								Path: "config.json",
							}},
						}}},
					},
				},
			},
		},
	}
	created, err := p.client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create BuildKit job: %w", err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		latest, err := p.client.BatchV1().Jobs(namespace).Get(ctx, created.Name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("read BuildKit job status: %w", err)
		}
		if latest.Status.Succeeded > 0 {
			logs, err := p.buildKitLogs(ctx, namespace, created.Name)
			if err != nil {
				return "", fmt.Errorf("%w: could not read BuildKit result: %v", errBuildFailed, err)
			}
			matches := buildkitDigestPattern.FindStringSubmatch(logs)
			if len(matches) != 2 {
				return "", fmt.Errorf("%w: BuildKit did not report the pushed image digest", errBuildFailed)
			}
			return imageTag + "@" + matches[1], nil
		}
		if latest.Status.Failed > 0 {
			return "", errBuildFailed
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for BuildKit job: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func buildkitBuildCommand() string {
	return `buildctl-daemonless.sh build --frontend dockerfile.v0 --local context=/workspace --local dockerfile=/workspace --opt "filename=$CRELAN_DOCKERFILE" --output "type=image,name=$CRELAN_IMAGE_TAG,push=true" --metadata-file /workspace/build-metadata.json && cat /workspace/build-metadata.json`
}

func (p *kubernetesProvisioner) buildKitLogs(ctx context.Context, namespace, jobName string) (string, error) {
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "batch.kubernetes.io/job-name=" + jobName,
	})
	if err != nil {
		return "", fmt.Errorf("list BuildKit pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", errors.New("no BuildKit pod was found")
	}

	request := p.client.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: "build", TailLines: ptr(int64(30))})
	stream, err := request.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("open BuildKit pod logs: %w", err)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, 8<<10))
	if err != nil {
		return "", fmt.Errorf("read BuildKit pod logs: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *kubernetesProvisioner) applyApplication(ctx context.Context, workspaceID int64, namespace string, request DeploymentRequest, image string) error {
	name := request.Name
	if request.CPUMillicores < 1 || request.MemoryMB < 1 {
		return errors.New("application CPU and memory limits must be positive")
	}
	if err := p.ensureWorkspaceResourceQuota(ctx, workspaceID, namespace, request.WorkspaceMaxApps); err != nil {
		return err
	}
	labels := map[string]string{
		workspaceLabel:   strconv.FormatInt(workspaceID, 10),
		applicationLabel: name,
	}
	containers := []corev1.Container{{
		Name:            name,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: int32(request.Port), Protocol: corev1.ProtocolTCP}},
		EnvFrom: []corev1.EnvFromSource{
			{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: applicationResourceName(name, "env")}, Optional: ptr(true)}},
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: applicationResourceName(name, "secrets")}, Optional: ptr(true)}},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot:             ptr(true),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resourceMustParse("100m"),
				corev1.ResourceMemory: resourceMustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resourceMustParse(fmt.Sprintf("%dm", request.CPUMillicores)),
				corev1.ResourceMemory: resourceMustParse(fmt.Sprintf("%dMi", request.MemoryMB)),
			},
		},
	}}
	podSpec := corev1.PodSpec{
		Containers: containers,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   ptr(true),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	if p.config.registrySecret != "" {
		podSpec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: p.config.registrySecret}}
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
	current, err := p.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
	} else if err == nil {
		deployment.ResourceVersion = current.ResourceVersion
		_, err = p.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply deployment: %w", err)
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt(request.Port), Protocol: corev1.ProtocolTCP}},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
	currentService, err := p.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	} else if err == nil {
		currentService.Labels = labels
		currentService.Spec.Selector = labels
		currentService.Spec.Ports = service.Spec.Ports
		_, err = p.client.CoreV1().Services(namespace).Update(ctx, currentService, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply service: %w", err)
	}
	if p.config.baseDomain != "" {
		return p.applyIngress(ctx, workspaceID, namespace, name, labels)
	}
	return nil
}

func (p *kubernetesProvisioner) ensureWorkspaceResourceQuota(ctx context.Context, workspaceID int64, namespace string, maxApps int) error {
	if maxApps < 1 {
		maxApps = 5
	}
	labels := map[string]string{workspaceLabel: strconv.FormatInt(workspaceID, 10)}
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "crelan-app-limits", Namespace: namespace, Labels: labels},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
			corev1.ResourceName("count/deployments.apps"): resource.MustParse(strconv.Itoa(maxApps)),
			corev1.ResourceServices:                       resource.MustParse(strconv.Itoa(maxApps)),
		}},
	}
	quotas := p.client.CoreV1().ResourceQuotas(namespace)
	current, err := quotas.Get(ctx, quota.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := quotas.Create(ctx, quota, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create workspace application quota: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load workspace application quota: %w", err)
	}
	if !ownsWorkspace(current.Labels, workspaceID) {
		return errors.New("workspace application quota is not owned by this workspace")
	}
	quota.ResourceVersion = current.ResourceVersion
	if _, err := quotas.Update(ctx, quota, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update workspace application quota: %w", err)
	}
	return nil
}

func (p *kubernetesProvisioner) applyIngress(ctx context.Context, workspaceID int64, namespace, name string, labels map[string]string) error {
	host := fmt.Sprintf("%s-ws-%d.%s", name, workspaceID, p.config.baseDomain)
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: networkingv1.IngressSpec{
			IngressClassName: optionalString(p.config.ingressClass),
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: name,
							Port: networkingv1.ServiceBackendPort{Number: 80},
						}},
					}},
				}},
			}},
		},
	}
	if p.config.tlsSecret != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: p.config.tlsSecret}}
	}
	current, err := p.client.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = p.client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, metav1.CreateOptions{})
	} else if err == nil {
		ingress.ResourceVersion = current.ResourceVersion
		_, err = p.client.NetworkingV1().Ingresses(namespace).Update(ctx, ingress, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply ingress: %w", err)
	}
	return nil
}

func (p *kubernetesProvisioner) ConfigureCustomDomain(ctx context.Context, workspaceID int64, appName, host, issuer string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	app, err := p.client.AppsV1().Deployments(namespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("load custom-domain application: %w", err)
	}
	if !ownsWorkspace(app.Labels, workspaceID) || app.Labels[applicationLabel] != appName {
		return errors.New("application is not owned by this workspace")
	}
	digest := sha256.Sum256([]byte(host))
	hash := hex.EncodeToString(digest[:6])
	ingressName := "crelan-domain-" + hash
	tlsName := "crelan-tls-" + hash
	pathType := networkingv1.PathTypePrefix
	annotations := map[string]string{
		"external-dns.alpha.kubernetes.io/hostname": host,
		"cert-manager.io/cluster-issuer":            issuer,
	}
	labels := map[string]string{workspaceLabel: strconv.FormatInt(workspaceID, 10), applicationLabel: appName}
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: ingressName, Namespace: namespace, Labels: labels, Annotations: annotations},
		Spec: networkingv1.IngressSpec{
			IngressClassName: optionalString(p.config.ingressClass),
			Rules: []networkingv1.IngressRule{{Host: host, IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
					Path: "/", PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: appName, Port: networkingv1.ServiceBackendPort{Number: 80},
					}},
				}}},
			}}},
			TLS: []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: tlsName}},
		},
	}
	ingresses := p.client.NetworkingV1().Ingresses(namespace)
	current, err := ingresses.Get(ctx, ingressName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = ingresses.Create(ctx, ingress, metav1.CreateOptions{})
	} else if err == nil {
		if !ownsWorkspace(current.Labels, workspaceID) {
			return errors.New("custom-domain ingress is not owned by this workspace")
		}
		ingress.ResourceVersion = current.ResourceVersion
		_, err = ingresses.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply custom-domain ingress: %w", err)
	}
	return nil
}

func (p *kubernetesProvisioner) DeleteCustomDomain(ctx context.Context, workspaceID int64, appName, host string) error {
	namespace, err := p.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(host))
	ingressName := "crelan-domain-" + hex.EncodeToString(digest[:6])
	ingresses := p.client.NetworkingV1().Ingresses(namespace)
	current, err := ingresses.Get(ctx, ingressName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load custom-domain ingress: %w", err)
	}
	if !ownsWorkspace(current.Labels, workspaceID) || current.Labels[applicationLabel] != appName {
		return errors.New("custom-domain ingress is not owned by this workspace")
	}
	if err := ingresses.Delete(ctx, ingressName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete custom-domain ingress: %w", err)
	}
	return nil
}

func (p *kubernetesProvisioner) waitForApplication(ctx context.Context, namespace, name string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		current, err := p.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read deployment status: %w", err)
		}
		if current.Status.AvailableReplicas > 0 {
			return nil
		}
		if current.Status.ObservedGeneration >= current.Generation && current.Status.Replicas > 0 &&
			current.Status.UnavailableReplicas > 0 && current.Status.UpdatedReplicas == 0 {
			return errors.New("application pod did not become ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *kubernetesProvisioner) applicationURL(workspaceID int64, name string) string {
	if p.config.baseDomain == "" {
		return ""
	}
	scheme := "http"
	if p.config.tlsSecret != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s-ws-%d.%s", scheme, name, workspaceID, p.config.baseDomain)
}

func buildResourceName(application string) (string, error) {
	suffix, err := randomToken(6)
	if err != nil {
		return "", fmt.Errorf("generate build identifier: %w", err)
	}
	name := strings.Trim(strings.ToLower(application), "-")
	if len(name) > 48 {
		name = name[:48]
	}
	return name + "-build-" + strings.ToLower(strings.ReplaceAll(suffix, "_", "a")), nil
}

func gitCloneArgs(request DeploymentRequest) []string {
	args := []string{"clone", "--depth", "1"}
	if request.Branch != "" {
		args = append(args, "--branch", request.Branch)
	}
	return append(args, "--", request.RepositoryURL, "/workspace")
}

func conditionMessage(condition map[string]any) string {
	message, _ := condition["message"].(string)
	reason, _ := condition["reason"].(string)
	if message == "" {
		return reason
	}
	if reason == "" {
		return message
	}
	return reason + ": " + message
}

func kpackGitSource(request DeploymentRequest) map[string]any {
	source := map[string]any{"url": request.RepositoryURL}
	if request.Branch != "" {
		source["revision"] = request.Branch
	}
	return source
}

func namespaceWorkspaceID(namespace, prefix string) string {
	return strings.TrimPrefix(namespace, prefix)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func ptr[T any](value T) *T {
	return &value
}

func resourceMustParse(value string) resource.Quantity {
	return resource.MustParse(value)
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getenv(key string) string {
	return os.Getenv(key)
}
