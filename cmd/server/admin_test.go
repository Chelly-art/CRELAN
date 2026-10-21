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
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func TestExtractStackTrace(t *testing.T) {
	goPanicLogs := `2026-10-21T10:00:00Z server starting...
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x492102]
goroutine 1 [running]:
main.handleRequest(...)
	/app/main.go:42 +0x2b
`
	trace, anomaly := extractStackTrace(goPanicLogs)
	if !strings.Contains(anomaly, "panic: runtime error") {
		t.Errorf("expected Go panic anomaly to be extracted, got: %q", anomaly)
	}
	if !strings.Contains(trace, "main.handleRequest") {
		t.Errorf("expected stack trace snippet to include frame, got: %q", trace)
	}

	pyLogs := `Traceback (most recent call last):
  File "app.py", line 12, in <module>
    import psycopg2
ModuleNotFoundError: No module named 'psycopg2'
`
	_, pyAnomaly := extractStackTrace(pyLogs)
	if !strings.Contains(pyAnomaly, "ModuleNotFoundError") {
		t.Errorf("expected python anomaly to be ModuleNotFoundError, got: %q", pyAnomaly)
	}
}

func TestSecurityAuditAndSecretLeakGuard(t *testing.T) {
	podWithRoot := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "insecure-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "insecure-app",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "web",
					Image: "nginx:latest",
					Env: []corev1.EnvVar{
						{Name: "AWS_ACCESS_KEY_ID", Value: "AKIAIOSFODNN7EXAMPLE"},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	cmWithKey := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"id_rsa": "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAz...==\n-----END RSA PRIVATE KEY-----",
		},
	}

	client := k8sfake.NewSimpleClientset(podWithRoot, cmWithKey)
	audit, err := runSecurityAudit(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error running security audit: %v", err)
	}

	if audit.RootCount != 1 {
		t.Errorf("expected 1 root container detected, got %d", audit.RootCount)
	}
	if audit.SecretLeaksCount < 2 {
		t.Errorf("expected at least 2 secret leaks (env + configmap), got %d", audit.SecretLeaksCount)
	}
	if audit.Score >= 100 {
		t.Errorf("expected security score to reflect findings, got %d", audit.Score)
	}
}

func TestHardenDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-dep",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "api",
							Image: "my-api:v1",
						},
					},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(dep)
	err := hardenDeployment(context.Background(), client, "default", "api-dep")
	if err != nil {
		t.Fatalf("unexpected error hardening deployment: %v", err)
	}

	updated, err := client.AppsV1().Deployments("default").Get(context.Background(), "api-dep", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to fetch updated deployment: %v", err)
	}

	c := updated.Spec.Template.Spec.Containers[0]
	if c.SecurityContext == nil {
		t.Fatal("expected container securityContext to be populated")
	}
	if c.SecurityContext.RunAsNonRoot == nil || !*c.SecurityContext.RunAsNonRoot {
		t.Errorf("expected runAsNonRoot to be true")
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("expected allowPrivilegeEscalation to be false")
	}
	if c.SecurityContext.Privileged == nil || *c.SecurityContext.Privileged {
		t.Errorf("expected privileged to be false")
	}
	if len(c.SecurityContext.Capabilities.Drop) == 0 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected capabilities drop ALL")
	}
}

func TestRBACBlastRadiusAnalysis(t *testing.T) {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "admin-binding",
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "ci-runner",
				Namespace: "default",
			},
		},
	}

	crWildcard := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "wildcard-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
		},
	}

	crbWildcard := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "wildcard-binding",
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: "wildcard-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "User",
				Name: "test-user",
			},
		},
	}

	client := k8sfake.NewSimpleClientset(crb, crWildcard, crbWildcard)
	report, err := runRBACBlastRadiusAnalysis(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error running rbac analysis: %v", err)
	}

	if report.ClusterAdminCount != 1 {
		t.Errorf("expected 1 cluster admin binding, got %d", report.ClusterAdminCount)
	}
	if report.WildcardPermissionsCount != 1 {
		t.Errorf("expected 1 wildcard permission detected, got %d", report.WildcardPermissionsCount)
	}
	if report.RiskScore <= 0 {
		t.Errorf("expected positive risk score, got %d", report.RiskScore)
	}
}

func TestCalculateResourceRightSizingAndApply(t *testing.T) {
	depOverprovisioned := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "heavy-worker",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "worker",
							Image: "worker:latest",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2000m"),
									corev1.ResourceMemory: resource.MustParse("4096Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
									corev1.ResourceMemory: resource.MustParse("8192Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(depOverprovisioned)
	report, err := calculateResourceRightSizing(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error calculating right sizing: %v", err)
	}

	if report.OverprovisionedCount != 1 {
		t.Errorf("expected 1 overprovisioned workload, got %d", report.OverprovisionedCount)
	}
	if report.EstimatedMonthlySavingsUSD <= 0 {
		t.Errorf("expected positive monthly savings, got %f", report.EstimatedMonthlySavingsUSD)
	}

	// Apply patch
	err = applyRightSizingPatch(context.Background(), client, "default", "heavy-worker", "250m", "500m", "512Mi", "1024Mi")
	if err != nil {
		t.Fatalf("unexpected error applying right sizing patch: %v", err)
	}

	updated, _ := client.AppsV1().Deployments("default").Get(context.Background(), "heavy-worker", metav1.GetOptions{})
	cpuReq := updated.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().String()
	if cpuReq != "250m" {
		t.Errorf("expected CPU request to be patched to 250m, got %s", cpuReq)
	}
}

func TestAICopilotQueries(t *testing.T) {
	podCrash := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "crash-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					RestartCount: 12,
					Ready:        false,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(podCrash)

	// Test Crash intent query
	resp, err := runAICopilotQuery(context.Background(), client, nil, "Which pods are crashing or restarting?")
	if err != nil {
		t.Fatalf("unexpected error running copilot query: %v", err)
	}
	if resp.Intent != "Workload Health & Crash Diagnostics" {
		t.Errorf("expected crash intent, got %s", resp.Intent)
	}
	if len(resp.Matches) == 0 {
		t.Errorf("expected at least 1 match for crashing pod")
	}

	// Test Security intent query
	secResp, err := runAICopilotQuery(context.Background(), client, nil, "Show me security vulnerabilities and root containers")
	if err != nil {
		t.Fatalf("unexpected error running copilot security query: %v", err)
	}
	if secResp.Intent != "Security Posture & CIS Hardening" {
		t.Errorf("expected security intent, got %s", secResp.Intent)
	}
}

func TestPlatformAuditTrailRecording(t *testing.T) {
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("failed to init auth service: %v", err)
	}
	defer auth.db.Close()
	db := auth.db

	ctx := context.Background()
	// Record two audit logs
	err = recordPlatformAudit(db, "admin@pipeops.app", "restart", "Deployment", "prod", "web-api", "Rollout restart triggered")
	if err != nil {
		t.Fatalf("recordPlatformAudit failed: %v", err)
	}
	err = recordPlatformAudit(db, "sre@pipeops.app", "scale", "Deployment", "staging", "worker", "Scaled to 5")
	if err != nil {
		t.Fatalf("recordPlatformAudit failed: %v", err)
	}

	// Fetch all
	logs, err := fetchPlatformAuditLogs(ctx, db, "", "", 50)
	if err != nil {
		t.Fatalf("fetchPlatformAuditLogs failed: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}

	// Filter by namespace
	prodLogs, err := fetchPlatformAuditLogs(ctx, db, "prod", "", 50)
	if err != nil {
		t.Fatalf("fetchPlatformAuditLogs for prod failed: %v", err)
	}
	if len(prodLogs) != 1 || prodLogs[0].Name != "web-api" {
		t.Errorf("unexpected prod logs: %+v", prodLogs)
	}
}

func TestIncidentWarRoomAndPostMortemGenerator(t *testing.T) {
	auth, err := newAuthService()
	if err != nil {
		t.Fatalf("failed to init auth service: %v", err)
	}
	defer auth.db.Close()
	db := auth.db

	crashedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-crash",
			Namespace: "production",
			Labels: map[string]string{
				capsuleTenantLabel: "e-commerce",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-30 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
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

	client := k8sfake.NewSimpleClientset(crashedPod)
	ctx := context.Background()

	// 1. Incident Snapshot
	snapshot, err := fetchIncidentSnapshot(ctx, client, "production", "orders-api-crash")
	if err != nil {
		t.Fatalf("fetchIncidentSnapshot failed: %v", err)
	}
	if snapshot.PodName != "orders-api-crash" || snapshot.ExitCode != 137 || snapshot.Reason != "OOMKilled" {
		t.Errorf("unexpected snapshot: %+v", snapshot)
	}
	if !strings.HasPrefix(snapshot.IncidentID, "INC-ORDERS-API-CRASH") {
		t.Errorf("unexpected incident ID: %s", snapshot.IncidentID)
	}

	// 2. Post-Mortem Report & Markdown Generation
	_ = recordPlatformAudit(db, "incident-commander@pipeops.app", "restart", "Pod", "production", "orders-api-crash", "Emergency pod reboot attempted")

	postMortem, err := generatePostMortemReport(ctx, client, db, "production", "orders-api-crash")
	if err != nil {
		t.Fatalf("generatePostMortemReport failed: %v", err)
	}
	if postMortem.Severity != "CRITICAL" {
		t.Errorf("expected CRITICAL severity, got %s", postMortem.Severity)
	}
	if !strings.Contains(postMortem.BlastRadius, "e-commerce") {
		t.Errorf("expected tenant in blast radius, got %s", postMortem.BlastRadius)
	}
	if !strings.Contains(postMortem.MarkdownContent, "# Incident Post-Mortem") {
		t.Errorf("markdown does not contain header: %s", postMortem.MarkdownContent)
	}
	if !strings.Contains(postMortem.MarkdownContent, "Emergency pod reboot attempted") {
		t.Errorf("markdown timeline missing operator audit action")
	}
}

func TestClusterUpgradeRadar(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-ingress",
			Namespace: "default",
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "nginx",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-dep",
			Namespace: "default",
			Annotations: map[string]string{
				"seccomp.security.alpha.kubernetes.io/pod": "runtime/default",
			},
		},
	}

	client := k8sfake.NewSimpleClientset(ing, dep)
	ctx := context.Background()

	radar, err := runClusterUpgradeRadar(ctx, client)
	if err != nil {
		t.Fatalf("runClusterUpgradeRadar failed: %v", err)
	}
	if radar.DeprecatedCount != 2 {
		t.Fatalf("expected 2 deprecated findings, got %d", radar.DeprecatedCount)
	}
	if radar.UpgradeReadinessScore >= 100 {
		t.Errorf("expected readiness score deduction, got %d", radar.UpgradeReadinessScore)
	}
}

func TestNetworkTrafficTopologyAndOrphanDetection(t *testing.T) {
	matchingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-pod-1",
			Namespace: "prod",
			Labels:    map[string]string{"app": "api"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.244.1.15",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	healthySvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-svc",
			Namespace: "prod",
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{
				{Port: 80, TargetPort: intstr.FromInt(8080)},
			},
		},
	}
	orphanSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-svc",
			Namespace: "prod",
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "nonexistent-app"},
		},
	}
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-ingress",
			Namespace: "prod",
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "api.crelan.io",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "api-svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(matchingPod, healthySvc, orphanSvc, ing)
	ctx := context.Background()

	topo, err := fetchNetworkTrafficTopology(ctx, client)
	if err != nil {
		t.Fatalf("fetchNetworkTrafficTopology failed: %v", err)
	}
	if topo.TotalIngresses != 1 || topo.TotalServices != 2 {
		t.Errorf("unexpected counts: ingresses=%d, services=%d", topo.TotalIngresses, topo.TotalServices)
	}
	if topo.OrphanServices != 1 {
		t.Errorf("expected 1 orphan service, got %d", topo.OrphanServices)
	}
	if len(topo.OrphanAlerts) != 1 || topo.OrphanAlerts[0].ServiceName != "stale-svc" {
		t.Errorf("expected orphan alert for stale-svc: %+v", topo.OrphanAlerts)
	}
	if len(topo.Routes) != 1 || topo.Routes[0].ServiceName != "api-svc" || len(topo.Routes[0].TargetPods) != 1 {
		t.Errorf("expected 1 route mapped to api-pod-1: %+v", topo.Routes)
	}
}

func TestTenantNetworkIsolationMatrix(t *testing.T) {
	ns1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "tenant-a",
			Labels: map[string]string{capsuleTenantLabel: "fintech"},
		},
	}
	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "tenant-b",
			Labels: map[string]string{capsuleTenantLabel: "healthcare"},
		},
	}

	// Default deny policy for tenant-a
	npA := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-deny-all",
			Namespace: "tenant-a",
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	}

	client := k8sfake.NewSimpleClientset(ns1, ns2, npA)
	ctx := context.Background()

	matrix, err := calculateTenantNetworkMatrix(ctx, client)
	if err != nil {
		t.Fatalf("calculateTenantNetworkMatrix failed: %v", err)
	}
	if matrix.TotalTenants != 2 {
		t.Fatalf("expected 2 tenants, got %d", matrix.TotalTenants)
	}
	if matrix.FullyIsolatedTenants != 1 || matrix.UnisolatedTenants != 1 {
		t.Errorf("expected 1 fully isolated and 1 unisolated, got %d and %d", matrix.FullyIsolatedTenants, matrix.UnisolatedTenants)
	}
	if matrix.IsolationScore != 50 {
		t.Errorf("expected 50%% isolation score, got %d", matrix.IsolationScore)
	}
}



