package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSRESLOCalculationAndBurnRate(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api-service", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api-service"}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-pod-1",
				Namespace: "default",
				Labels:    map[string]string{"app": "api-service"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "api", RestartCount: 3},
				},
			},
		},
	)

	summary, err := fetchSRESLOsWithClient(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("fetchSRESLOsWithClient failed: %v", err)
	}

	if summary.TotalSLOs == 0 {
		t.Fatalf("expected at least 1 SLO, got %d", summary.TotalSLOs)
	}

	slo := summary.SLOs[0]
	if slo.ServiceName != "api-service" {
		t.Errorf("expected service api-service, got %s", slo.ServiceName)
	}
	if slo.TargetSLI != 99.9 {
		t.Errorf("expected default target 99.9, got %f", slo.TargetSLI)
	}
	// Pod had 3 restarts so actual error is present
	if slo.ErrorBudgetRemaining >= 100.0 {
		t.Errorf("expected error budget to be partially consumed, got %f", slo.ErrorBudgetRemaining)
	}
}

func TestSREGoldenSignalsRED(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web-svc", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-pod-1", Namespace: "default", Labels: map[string]string{"app": "web"}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	summary, err := fetchSREGoldenSignalsWithClient(context.Background(), client)
	if err != nil {
		t.Fatalf("fetchSREGoldenSignalsWithClient failed: %v", err)
	}

	if summary.TotalServices != 1 {
		t.Fatalf("expected 1 service, got %d", summary.TotalServices)
	}

	sig := summary.Signals[0]
	if sig.ServiceName != "web-svc" {
		t.Errorf("expected web-svc, got %s", sig.ServiceName)
	}
	if sig.RateRPS <= 0 {
		t.Errorf("expected positive RPS, got %f", sig.RateRPS)
	}
	if sig.LatencyP99Ms <= 0 {
		t.Errorf("expected positive P99 latency, got %f", sig.LatencyP99Ms)
	}
	if sig.Status != "healthy" {
		t.Errorf("expected healthy status, got %s", sig.Status)
	}
}

func TestSREProbeDoctorAntiPatterns(t *testing.T) {
	var reps int32 = 2
	client := fake.NewSimpleClientset(
		// Deployment with Missing Readiness Probe & Aggressive Liveness Timeout
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "misconfigured-app", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &reps,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "app",
								// Missing ReadinessProbe
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8080)},
									},
									TimeoutSeconds: 1, // Aggressive timeout
								},
							},
						},
					},
				},
			},
		},
		// Deployment with Identical Liveness & Readiness endpoints
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cascading-app", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &reps,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "web",
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{Path: "/status", Port: intstr.FromInt(80)},
									},
								},
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										HTTPGet: &corev1.HTTPGetAction{Path: "/status", Port: intstr.FromInt(80)},
									},
								},
							},
						},
					},
				},
			},
		},
	)

	report, err := fetchSREProbeAuditWithClient(context.Background(), client)
	if err != nil {
		t.Fatalf("fetchSREProbeAuditWithClient failed: %v", err)
	}

	if report.TotalWorkloads != 2 {
		t.Fatalf("expected 2 workloads, got %d", report.TotalWorkloads)
	}

	foundMissingReadiness := false
	foundIdenticalProbes := false

	for _, issue := range report.Issues {
		if issue.IssueType == "missing_readiness" && issue.WorkloadName == "misconfigured-app" {
			foundMissingReadiness = true
		}
		if issue.IssueType == "identical_probes" && issue.WorkloadName == "cascading-app" {
			foundIdenticalProbes = true
		}
	}

	if !foundMissingReadiness {
		t.Errorf("expected missing_readiness issue for misconfigured-app")
	}
	if !foundIdenticalProbes {
		t.Errorf("expected identical_probes issue for cascading-app")
	}
	if report.CriticalCount == 0 {
		t.Errorf("expected critical issues to be reported")
	}
}

func TestSREResilienceAndPDBAudit(t *testing.T) {
	var reps int32 = 3
	minAvail := intstr.FromInt(1)

	client := fake.NewSimpleClientset(
		// Node in zone-a
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-1",
				Labels: map[string]string{"topology.kubernetes.io/zone": "us-east-1a"},
			},
		},
		// Deployment with 3 replicas and PDB
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "protected-dep", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &reps,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "protected"}},
			},
		},
		&policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "protected-pdb", Namespace: "default"},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable: &minAvail,
				Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": "protected"}},
			},
		},
		// Multi-replica deployment WITHOUT PDB
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "unprotected-dep", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &reps,
			},
		},
	)

	report, err := fetchSREResilienceAuditWithClient(context.Background(), client)
	if err != nil {
		t.Fatalf("fetchSREResilienceAuditWithClient failed: %v", err)
	}

	if report.TotalDeployments != 2 {
		t.Fatalf("expected 2 deployments, got %d", report.TotalDeployments)
	}
	if report.CoveredByPDBCount != 1 {
		t.Errorf("expected 1 deployment covered by PDB, got %d", report.CoveredByPDBCount)
	}
	if report.MissingPDBCount != 1 {
		t.Errorf("expected 1 deployment missing PDB, got %d", report.MissingPDBCount)
	}
}

func TestSREReliabilityScorecards(t *testing.T) {
	var reps int32 = 2
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy-service", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &reps,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "main",
								LivenessProbe:  &corev1.Probe{},
								ReadinessProbe: &corev1.Probe{},
							},
						},
					},
				},
			},
		},
		&policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy-pdb", Namespace: "default"},
		},
	)

	summary, err := fetchSREReliabilityScorecardsWithClient(context.Background(), client)
	if err != nil {
		t.Fatalf("fetchSREReliabilityScorecardsWithClient failed: %v", err)
	}

	if summary.TotalServices != 1 {
		t.Fatalf("expected 1 service scorecard, got %d", summary.TotalServices)
	}

	sc := summary.Scorecards[0]
	if sc.OverallScore < 80 {
		t.Errorf("expected high score for healthy service, got %d", sc.OverallScore)
	}
	if sc.OverallGrade != "A" && sc.OverallGrade != "A+" {
		t.Errorf("expected Grade A or A+, got %s", sc.OverallGrade)
	}
}

func TestSREEndpointsAuth(t *testing.T) {
	auth := &authService{demoLoginEnabled: false}
	admin := newAdminService(auth)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/sre/slos"},
		{"POST", "/api/v1/admin/sre/slos"},
		{"DELETE", "/api/v1/admin/sre/slos/1"},
		{"GET", "/api/v1/admin/sre/golden-signals"},
		{"GET", "/api/v1/admin/sre/probes/audit"},
		{"GET", "/api/v1/admin/sre/resilience/audit"},
		{"GET", "/api/v1/admin/sre/canary"},
		{"POST", "/api/v1/admin/sre/canary/abort"},
		{"GET", "/api/v1/admin/sre/chaos/experiments"},
		{"POST", "/api/v1/admin/sre/chaos/run"},
		{"GET", "/api/v1/admin/sre/scorecards"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		switch {
		case ep.path == "/api/v1/admin/sre/slos" && ep.method == "GET":
			admin.getSRESLOs(rec, req)
		case ep.path == "/api/v1/admin/sre/slos" && ep.method == "POST":
			admin.createSRESLO(rec, req)
		case strings.HasPrefix(ep.path, "/api/v1/admin/sre/slos/") && ep.method == "DELETE":
			admin.deleteSRESLO(rec, req)
		case ep.path == "/api/v1/admin/sre/golden-signals":
			admin.getSREGoldenSignals(rec, req)
		case ep.path == "/api/v1/admin/sre/probes/audit":
			admin.getSREProbeAudit(rec, req)
		case ep.path == "/api/v1/admin/sre/resilience/audit":
			admin.getSREResilienceAudit(rec, req)
		case ep.path == "/api/v1/admin/sre/canary":
			admin.getSRECanaries(rec, req)
		case ep.path == "/api/v1/admin/sre/canary/abort":
			admin.abortSRECanary(rec, req)
		case ep.path == "/api/v1/admin/sre/chaos/experiments":
			admin.getSREChaosExperiments(rec, req)
		case ep.path == "/api/v1/admin/sre/chaos/run":
			admin.runSREChaosExperiment(rec, req)
		case ep.path == "/api/v1/admin/sre/scorecards":
			admin.getSREReliabilityScorecards(rec, req)
		}

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for unauthorized endpoint %s %s, got %d", ep.method, ep.path, rec.Code)
		}
	}
}
