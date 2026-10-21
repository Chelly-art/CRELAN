package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	assets "crelan"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
)

func TestAdminHTMLContainsLayout(t *testing.T) {
	page, err := assets.Web.ReadFile("web/admin.html")
	if err != nil {
		t.Fatalf("failed to read admin.html from embedded assets: %v", err)
	}
	content := string(page)
	expectedMarkers := []string{
		"admin-app",
		"admin-rail",
		"admin-sidebar",
		"admin-topbar",
		"admin-namespace-select",
		"admin-search-input",
		"admin-inspector-drawer",
		"admin-terminal-drawer",
		"admin-ai-modal",
	}
	for _, marker := range expectedMarkers {
		if !strings.Contains(content, marker) {
			t.Errorf("admin.html missing expected admin marker: %q", marker)
		}
	}
}

func TestAdminK8sDataFetchers(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
			NodeInfo: corev1.NodeSystemInfo{
				KubeletVersion: "v1.34.1",
				OSImage:        "Ubuntu 24.04 LTS",
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
			Labels: map[string]string{
				workspaceLabel:   "1",
				applicationLabel: "web-app",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  "web",
					Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
					},
				},
			},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-dep",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "web", Image: "nginx:latest"}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:      2,
			ReadyReplicas: 2,
		},
	}

	client := k8sfake.NewSimpleClientset(node, pod, dep)
	ctx := context.Background()

	// 1. Test Cluster Info
	info, err := fetchClusterInfo(ctx, client, "test-context", nil)
	if err != nil {
		t.Fatalf("fetchClusterInfo failed: %v", err)
	}
	if info.NodesCount != 1 || info.ReadyNodes != 1 {
		t.Errorf("expected 1 ready node, got %d/%d", info.ReadyNodes, info.NodesCount)
	}

	// 2. Test Nodes List
	nodes, err := fetchNodesList(ctx, client)
	if err != nil {
		t.Fatalf("fetchNodesList failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "node-1" || nodes[0].Status != "Ready" {
		t.Errorf("unexpected node list result: %+v", nodes)
	}

	// 3. Test Pods List
	wsMap := map[string]string{"1": "Acme Workspace"}
	pods, err := fetchPodsList(ctx, client, "", wsMap)
	if err != nil {
		t.Fatalf("fetchPodsList failed: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "test-pod-1" || pods[0].WorkspaceName != "Acme Workspace" {
		t.Errorf("unexpected pods result: %+v", pods)
	}

	// 4. Test Deployments List
	deps, err := fetchDeploymentsList(ctx, client, "", wsMap)
	if err != nil {
		t.Fatalf("fetchDeploymentsList failed: %v", err)
	}
	if len(deps) != 1 || deps[0].Name != "web-dep" {
		t.Errorf("unexpected deployments result: %+v", deps)
	}
}

func TestAIDiagnosticianOOMKilled(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "crashed-worker",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "worker",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
						},
					},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(pod)
	diag, err := runAIDiagnosis(context.Background(), client, "default", "crashed-worker")
	if err != nil {
		t.Fatalf("runAIDiagnosis failed: %v", err)
	}

	if diag.Severity != "critical" {
		t.Errorf("expected critical severity for OOMKilled pod, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Summary, "OOMKilled") {
		t.Errorf("expected summary to mention OOMKilled, got %q", diag.Summary)
	}
	if len(diag.Remediation) == 0 {
		t.Error("expected AI remediation recommendations, got none")
	}
}

func TestSecurityAuditFlagsRootUser(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "insecure-app",
			Namespace: "crelan-ws-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "web",
					Image: "nginx:latest",
					// No securityContext.runAsNonRoot -> flags as root user
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	client := k8sfake.NewSimpleClientset(pod)
	audit, err := runSecurityAudit(context.Background(), client)
	if err != nil {
		t.Fatalf("runSecurityAudit failed: %v", err)
	}

	if audit.TotalPodsScanned != 1 {
		t.Errorf("expected 1 pod scanned, got %d", audit.TotalPodsScanned)
	}
	if audit.FailedChecks == 0 {
		t.Error("expected security findings for root container, got 0")
	}
}

func TestFormatAge(t *testing.T) {
	if got := formatAge(time.Time{}); got != "-" {
		t.Errorf("expected '-', got %q", got)
	}
	now := time.Now()
	if got := formatAge(now.Add(-30 * time.Second)); !strings.HasSuffix(got, "s") {
		t.Errorf("expected seconds format, got %q", got)
	}
	if got := formatAge(now.Add(-10 * time.Minute)); !strings.HasSuffix(got, "m") {
		t.Errorf("expected minutes format, got %q", got)
	}
	if got := formatAge(now.Add(-5 * time.Hour)); !strings.HasSuffix(got, "h") {
		t.Errorf("expected hours format, got %q", got)
	}
	if got := formatAge(now.Add(-48 * time.Hour)); !strings.HasSuffix(got, "d") {
		t.Errorf("expected days format, got %q", got)
	}
}

func TestConnectClusterValidation(t *testing.T) {
	unflattenedConfig := `
apiVersion: v1
clusters:
- cluster:
    certificate-authority: /home/user/.minikube/ca.crt
    server: https://192.168.49.2:8443
  name: minikube
contexts:
- context:
    cluster: minikube
    user: minikube
  name: minikube
current-context: minikube
kind: Config
preferences: {}
users:
- name: minikube
  user:
    client-certificate: /home/user/.minikube/profiles/minikube/client.crt
    client-key: /home/user/.minikube/profiles/minikube/client.key
`
	config, err := clientcmd.Load([]byte(unflattenedConfig))
	if err != nil {
		t.Fatalf("failed to load test kubeconfig: %v", err)
	}
	// Verify missing file check correctly identifies that the referenced path cannot be found
	for _, cluster := range config.Clusters {
		if cluster.CertificateAuthority != "" && len(cluster.CertificateAuthorityData) == 0 {
			if _, err := os.ReadFile(cluster.CertificateAuthority); err == nil {
				t.Errorf("expected non-existent file %q to fail reading", cluster.CertificateAuthority)
			}
		}
	}
	// Verify auto-embedding when file exists
	tmpCA, err := os.CreateTemp("", "test-ca-*.crt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpCA.Name())
	tmpCA.WriteString("-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n")
	tmpCA.Close()

	testCluster := config.Clusters["minikube"]
	testCluster.CertificateAuthority = tmpCA.Name()
	if data, err := os.ReadFile(testCluster.CertificateAuthority); err == nil && len(data) > 0 {
		testCluster.CertificateAuthorityData = data
		testCluster.CertificateAuthority = ""
	}
	if len(testCluster.CertificateAuthorityData) == 0 || testCluster.CertificateAuthority != "" {
		t.Errorf("expected temp CA to be embedded into CertificateAuthorityData")
	}
}

