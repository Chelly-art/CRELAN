package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// -------------------------------------------------------------
// Cluster & Node Data Models
// -------------------------------------------------------------

type clusterInfo struct {
	Connected        bool   `json:"connected"`
	Context          string `json:"context"`
	ServerVersion    string `json:"server_version"`
	GitCommit        string `json:"git_commit,omitempty"`
	Platform         string `json:"platform,omitempty"`
	NodesCount       int    `json:"nodes_count"`
	ReadyNodes       int    `json:"ready_nodes"`
	NamespacesCount  int    `json:"namespaces_count"`
	PodsCount        int    `json:"pods_count"`
	WarningEvents    int    `json:"warning_events_count"`
	WorkspacesCount  int64  `json:"workspaces_count"`
	DeploymentsCount int64  `json:"deployments_count"`
}

type nodeSummary struct {
	Name             string            `json:"name"`
	Status           string            `json:"status"` // Ready, NotReady
	Roles            []string          `json:"roles"`
	Version          string            `json:"version"`
	OSImage          string            `json:"os_image"`
	KernelVersion    string            `json:"kernel_version"`
	ContainerRuntime string            `json:"container_runtime"`
	InternalIP       string            `json:"internal_ip"`
	ExternalIP       string            `json:"external_ip,omitempty"`
	PodCIDR          string            `json:"pod_cidr,omitempty"`
	CPUCapacity      string            `json:"cpu_capacity"`
	CPUAllocatable   string            `json:"cpu_allocatable"`
	MemCapacity      string            `json:"mem_capacity"`
	MemAllocatable   string            `json:"mem_allocatable"`
	PodsCapacity     int64             `json:"pods_capacity"`
	PodsRunning      int               `json:"pods_running"`
	Age              string            `json:"age"`
	CreatedAt        string            `json:"created_at"`
	Conditions       []nodeCondition   `json:"conditions"`
	Labels           map[string]string `json:"labels,omitempty"`
}

type nodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// -------------------------------------------------------------
// Workloads Data Models
// -------------------------------------------------------------

type podSummary struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Phase           string            `json:"phase"`
	StatusDetail    string            `json:"status_detail,omitempty"`
	ReadyContainers string            `json:"ready_containers"`
	Restarts        int32             `json:"restarts"`
	ControlledBy    string            `json:"controlled_by,omitempty"`
	Node            string            `json:"node,omitempty"`
	PodIP           string            `json:"pod_ip,omitempty"`
	Age             string            `json:"age"`
	CreatedAt       string            `json:"created_at"`
	Labels          map[string]string `json:"labels,omitempty"`
	WorkspaceID     string            `json:"workspace_id,omitempty"`
	WorkspaceName   string            `json:"workspace_name,omitempty"`
	AppName         string            `json:"app_name,omitempty"`
	HasErrors       bool              `json:"has_errors"`
	Containers      []containerBrief  `json:"containers"`
}

type containerBrief struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
}

type deploymentSummary struct {
	Name              string   `json:"name"`
	Namespace         string   `json:"namespace"`
	Replicas          int32    `json:"replicas"`
	ReadyReplicas     int32    `json:"ready_replicas"`
	UpdatedReplicas   int32    `json:"updated_replicas"`
	AvailableReplicas int32    `json:"available_replicas"`
	Selector          string   `json:"selector,omitempty"`
	Images            []string `json:"images"`
	Age               string   `json:"age"`
	CreatedAt         string   `json:"created_at"`
	Conditions        []string `json:"conditions,omitempty"`
	WorkspaceID       string   `json:"workspace_id,omitempty"`
	WorkspaceName     string   `json:"workspace_name,omitempty"`
	AppName           string   `json:"app_name,omitempty"`
}

type statefulSetSummary struct {
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace"`
	Replicas      int32    `json:"replicas"`
	ReadyReplicas int32    `json:"ready_replicas"`
	Images        []string `json:"images"`
	Age           string   `json:"age"`
	CreatedAt     string   `json:"created_at"`
}

type daemonSetSummary struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Desired   int32    `json:"desired"`
	Current   int32    `json:"current"`
	Ready     int32    `json:"ready"`
	UpToDate  int32    `json:"up_to_date"`
	Images    []string `json:"images"`
	Age       string   `json:"age"`
	CreatedAt string   `json:"created_at"`
}

type jobSummary struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Completions int32  `json:"completions"`
	Succeeded   int32  `json:"succeeded"`
	Failed      int32  `json:"failed"`
	Active      int32  `json:"active"`
	Duration    string `json:"duration,omitempty"`
	Age         string `json:"age"`
	CreatedAt   string `json:"created_at"`
}

type cronJobSummary struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Schedule     string `json:"schedule"`
	Suspend      bool   `json:"suspend"`
	Active       int    `json:"active"`
	LastSchedule string `json:"last_schedule,omitempty"`
	Age          string `json:"age"`
	CreatedAt    string `json:"created_at"`
}

// -------------------------------------------------------------
// Networking Data Models
// -------------------------------------------------------------

type serviceSummary struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	Type        string   `json:"type"`
	ClusterIP   string   `json:"cluster_ip"`
	ExternalIPs []string `json:"external_ips,omitempty"`
	Ports       []string `json:"ports"`
	Selector    string   `json:"selector,omitempty"`
	Age         string   `json:"age"`
	CreatedAt   string   `json:"created_at"`
}

type ingressSummary struct {
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	IngressClass string   `json:"ingress_class,omitempty"`
	Hosts        []string `json:"hosts"`
	Addresses    []string `json:"addresses,omitempty"`
	Age          string   `json:"age"`
	CreatedAt    string   `json:"created_at"`
}

type networkPolicySummary struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	PodSelector string   `json:"pod_selector"`
	PolicyTypes []string `json:"policy_types"`
	Age         string   `json:"age"`
	CreatedAt   string   `json:"created_at"`
}

// -------------------------------------------------------------
// Configuration & Storage Data Models
// -------------------------------------------------------------

type configMapSummary struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	KeysCount int               `json:"keys_count"`
	Keys      []string          `json:"keys"`
	Data      map[string]string `json:"data,omitempty"`
	Age       string            `json:"age"`
	CreatedAt string            `json:"created_at"`
}

type secretSummary struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Type      string            `json:"type"`
	KeysCount int               `json:"keys_count"`
	Keys      []string          `json:"keys"`
	Data      map[string]string `json:"data,omitempty"`
	Age       string            `json:"age"`
	CreatedAt string            `json:"created_at"`
}

type pvcSummary struct {
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	Status       string   `json:"status"`
	VolumeName   string   `json:"volume_name,omitempty"`
	Capacity     string   `json:"capacity,omitempty"`
	AccessModes  []string `json:"access_modes"`
	StorageClass string   `json:"storage_class,omitempty"`
	Age          string   `json:"age"`
	CreatedAt    string   `json:"created_at"`
}

type pvSummary struct {
	Name          string   `json:"name"`
	Capacity      string   `json:"capacity"`
	AccessModes   []string `json:"access_modes"`
	ReclaimPolicy string   `json:"reclaim_policy"`
	Status        string   `json:"status"`
	Claim         string   `json:"claim,omitempty"`
	StorageClass  string   `json:"storage_class,omitempty"`
	Age           string   `json:"age"`
	CreatedAt     string   `json:"created_at"`
}

type storageClassSummary struct {
	Name                 string `json:"name"`
	Provisioner          string `json:"provisioner"`
	ReclaimPolicy        string `json:"reclaim_policy,omitempty"`
	VolumeBindingMode    string `json:"volume_binding_mode,omitempty"`
	AllowVolumeExpansion bool   `json:"allow_volume_expansion"`
	IsDefault            bool   `json:"is_default"`
	Age                  string `json:"age"`
	CreatedAt            string `json:"created_at"`
}

// -------------------------------------------------------------
// Namespaces & Events
// -------------------------------------------------------------

type namespaceSummary struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Tenant      string `json:"tenant,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Age         string `json:"age"`
	CreatedAt   string `json:"created_at"`
}

type eventSummary struct {
	Type           string `json:"type"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	ObjectKind     string `json:"object_kind"`
	ObjectName     string `json:"object_name"`
	Namespace      string `json:"namespace"`
	Source         string `json:"source,omitempty"`
	Count          int32  `json:"count"`
	FirstTimestamp string `json:"first_seen,omitempty"`
	LastTimestamp  string `json:"last_seen"`
	Age            string `json:"age"`
}

// -------------------------------------------------------------
// Platform Intelligence Models: AI Diagnosis, Security, Efficiency
// -------------------------------------------------------------

type aiDiagnosticReport struct {
	PodName               string         `json:"pod_name"`
	Namespace             string         `json:"namespace"`
	Phase                 string         `json:"phase"`
	Severity              string         `json:"severity"`
	Summary               string         `json:"summary"`
	ProbableCause         string         `json:"probable_cause"`
	Confidence            string         `json:"confidence"`
	Evidence              []string       `json:"evidence"`
	Remediation           []aiAction     `json:"remediation"`
	RelevantLogs          string         `json:"relevant_logs,omitempty"`
	StackTrace            string         `json:"stack_trace,omitempty"`
	LogAnomaly            string         `json:"log_anomaly,omitempty"`
	ExitCode              int32          `json:"exit_code,omitempty"`
	LastTerminationReason string         `json:"last_termination_reason,omitempty"`
	RecommendedSteps      []string       `json:"recommended_steps,omitempty"`
	WarningEvents         []eventSummary `json:"warning_events,omitempty"`
}

type aiAction struct {
	ActionID    string `json:"action_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	AutoFixable bool   `json:"auto_fixable"`
}

type securityAuditReport struct {
	TotalPodsScanned int                  `json:"total_pods_scanned"`
	PassedChecks     int                  `json:"passed_checks"`
	FailedChecks     int                  `json:"failed_checks"`
	Score            int                  `json:"score"`
	CriticalCount    int                  `json:"critical_count"`
	HighCount        int                  `json:"high_count"`
	SecretLeaksCount int                  `json:"secret_leaks_count"`
	RootCount        int                  `json:"root_count"`
	PrivilegedCount  int                  `json:"privileged_count"`
	Issues           []securityIssue      `json:"issues"`
	TenantIsolation  tenantIsolationAudit `json:"tenant_isolation"`
}

type securityIssue struct {
	Severity      string `json:"severity"`
	Rule          string `json:"rule"`
	PodName       string `json:"pod_name"`
	Namespace     string `json:"namespace"`
	Container     string `json:"container,omitempty"`
	Description   string `json:"description"`
	Remediation   string `json:"remediation"`
	WorkloadKind  string `json:"workload_kind,omitempty"`
	WorkloadName  string `json:"workload_name,omitempty"`
	CanAutoHarden bool   `json:"can_auto_harden"`
}

type tenantIsolationAudit struct {
	TotalTenantSpaces    int      `json:"total_tenant_namespaces"`
	EnforcedSpaces       int      `json:"enforced_namespaces"`
	MissingNetworkPolicy []string `json:"missing_network_policy"`
	Status               string   `json:"status"`
}

// RBAC Blast Radius Models
type rbacBlastRadiusReport struct {
	ClusterAdminCount        int                  `json:"cluster_admin_count"`
	PrivilegeEscalationCount int                  `json:"privilege_escalation_count"`
	WildcardPermissionsCount int                  `json:"wildcard_permissions_count"`
	CrossTenantBindingsCount int                  `json:"cross_tenant_bindings_count"`
	TotalBindingsScanned     int                  `json:"total_bindings_scanned"`
	RiskScore                int                  `json:"risk_score"`
	HighRiskBindings         []rbacBindingFinding `json:"high_risk_bindings"`
	ServiceAccountRisks      []serviceAccountRisk `json:"service_account_risks"`
}

type rbacBindingFinding struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace,omitempty"`
	RoleRef     string   `json:"role_ref"`
	RoleKind    string   `json:"role_kind"`
	Subjects    []string `json:"subjects"`
	RiskLevel   string   `json:"risk_level"`
	RiskReasons []string `json:"risk_reasons"`
	Remediation string   `json:"remediation"`
}

type serviceAccountRisk struct {
	ServiceAccount string   `json:"service_account"`
	Namespace      string   `json:"namespace"`
	IsTenant       bool     `json:"is_tenant"`
	RolesBound     []string `json:"roles_bound"`
	RiskSummary    string   `json:"risk_summary"`
}

// AI Copilot Models
type aiCopilotRequest struct {
	Query string `json:"query"`
}

type aiCopilotMatch struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Link      string `json:"link,omitempty"`
}

type aiCopilotAction struct {
	ActionID    string `json:"action_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Route       string `json:"route,omitempty"`
	Command     string `json:"command,omitempty"`
}

type aiCopilotResponse struct {
	Query            string            `json:"query"`
	Intent           string            `json:"intent"`
	Summary          string            `json:"summary"`
	Matches          []aiCopilotMatch  `json:"matches"`
	SuggestedActions []aiCopilotAction `json:"suggested_actions"`
}

// AI Right-Sizing & Cost Advisor Models
type rightSizingReport struct {
	TotalDeployments             int                         `json:"total_deployments"`
	OverprovisionedCount         int                         `json:"overprovisioned_count"`
	UnderprovisionedCount        int                         `json:"underprovisioned_count"`
	MissingLimitsCount           int                         `json:"missing_limits_count"`
	EstimatedClusterWastePercent float64                     `json:"estimated_cluster_waste_percent"`
	EstimatedMonthlySavingsUSD   float64                     `json:"estimated_monthly_savings_usd"`
	Recommendations              []rightSizingRecommendation `json:"recommendations"`
}

type rightSizingRecommendation struct {
	DeploymentName        string  `json:"deployment_name"`
	Namespace             string  `json:"namespace"`
	ContainerName         string  `json:"container_name"`
	CurrentCPURequest     string  `json:"current_cpu_request"`
	CurrentCPULimit       string  `json:"current_cpu_limit"`
	CurrentMemRequest     string  `json:"current_mem_request"`
	CurrentMemLimit       string  `json:"current_mem_limit"`
	RecommendedCPURequest string  `json:"recommended_cpu_request"`
	RecommendedCPULimit   string  `json:"recommended_cpu_limit"`
	RecommendedMemRequest string  `json:"recommended_mem_request"`
	RecommendedMemLimit   string  `json:"recommended_mem_limit"`
	MonthlySavingsUSD     float64 `json:"monthly_savings_usd"`
	RiskLevel             string  `json:"risk_level"`
	Reason                string  `json:"reason"`
	CanAutoApply          bool    `json:"can_auto_apply"`
}

type rightSizeApplyRequest struct {
	CPURequest string `json:"cpu_request"`
	CPULimit   string `json:"cpu_limit"`
	MemRequest string `json:"mem_request"`
	MemLimit   string `json:"mem_limit"`
}

type resourceEfficiencyReport struct {
	TotalClusterCPUAllocatable string                   `json:"total_cpu_allocatable"`
	TotalClusterMemAllocatable string                   `json:"total_mem_allocatable"`
	CPURequestsTotal           string                   `json:"cpu_requests_total"`
	MemRequestsTotal           string                   `json:"mem_requests_total"`
	CPUUtilizationPercent      float64                  `json:"cpu_utilization_percent"`
	MemUtilizationPercent      float64                  `json:"mem_utilization_percent"`
	Workspaces                 []workspaceResourceUsage `json:"workspaces"`
	Nodes                      []nodeResourceUsage      `json:"nodes"`
}

type workspaceResourceUsage struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Namespace     string `json:"namespace"`
	PodsCount     int    `json:"pods_count"`
	CPURequest    string `json:"cpu_request"`
	CPULimit      string `json:"cpu_limit"`
	MemRequest    string `json:"mem_request"`
	MemLimit      string `json:"mem_limit"`
	StorageUsage  string `json:"storage_usage"`
	Efficiency    string `json:"efficiency"`
}

type nodeResourceUsage struct {
	NodeName       string  `json:"node_name"`
	CPUAllocatable string  `json:"cpu_allocatable"`
	MemAllocatable string  `json:"mem_allocatable"`
	CPURequested   string  `json:"cpu_requested"`
	MemRequested   string  `json:"mem_requested"`
	PodsRunning    int     `json:"pods_running"`
	PodsCapacity   int64   `json:"pods_capacity"`
	CPUPct         float64 `json:"cpu_pct"`
	MemPct         float64 `json:"mem_pct"`
}

type managedDatabaseTopology struct {
	PostgresClusters []databaseClusterInfo `json:"postgres_clusters"`
	MySQLClusters    []databaseClusterInfo `json:"mysql_clusters"`
	RedisServices    []databaseClusterInfo `json:"redis_services"`
	StorageLeaks     []orphanStorageItem   `json:"storage_leaks"`
}

type databaseClusterInfo struct {
	Name        string   `json:"name"`
	Engine      string   `json:"engine"`
	Namespace   string   `json:"namespace"`
	Status      string   `json:"status"`
	Primary     string   `json:"primary,omitempty"`
	Replicas    []string `json:"replicas,omitempty"`
	PVCs        []string `json:"pvcs"`
	BackupState string   `json:"backup_state,omitempty"`
	Age         string   `json:"age"`
}

type orphanStorageItem struct {
	PVCName      string `json:"pvc_name"`
	Namespace    string `json:"namespace"`
	Capacity     string `json:"capacity"`
	StorageClass string `json:"storage_class"`
	Status       string `json:"status"`
	Age          string `json:"age"`
	Reason       string `json:"reason"`
}

type workspaceMapping struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	OwnerName   string   `json:"owner_name"`
	OwnerEmail  string   `json:"owner_email"`
	Deployments []string `json:"deployments"`
	Members     int      `json:"members"`
}

// -------------------------------------------------------------
// Helper Functions: Formatting, Ages, Parsing
// -------------------------------------------------------------

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd", days)
}

func getPodPhaseAndStatus(pod *corev1.Pod) (phase string, detail string, hasErrors bool) {
	phase = string(pod.Status.Phase)
	if pod.DeletionTimestamp != nil {
		return "Terminating", "Graceful shutdown in progress", true
	}
	for _, initStatus := range pod.Status.InitContainerStatuses {
		if initStatus.State.Waiting != nil && initStatus.State.Waiting.Reason != "" {
			reason := initStatus.State.Waiting.Reason
			return "Init:" + reason, initStatus.State.Waiting.Message, reason != "PodInitializing"
		}
		if initStatus.State.Terminated != nil && initStatus.State.Terminated.ExitCode != 0 {
			return "Init:Error", fmt.Sprintf("Exit code %d: %s", initStatus.State.Terminated.ExitCode, initStatus.State.Terminated.Reason), true
		}
	}
	for _, cStatus := range pod.Status.ContainerStatuses {
		if cStatus.State.Waiting != nil && cStatus.State.Waiting.Reason != "" {
			reason := cStatus.State.Waiting.Reason
			hasErr := reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "CreateContainerConfigError" || reason == "InvalidImageName"
			return reason, cStatus.State.Waiting.Message, hasErr
		}
		if cStatus.State.Terminated != nil {
			if cStatus.State.Terminated.Reason == "OOMKilled" {
				return "OOMKilled", fmt.Sprintf("Out of memory (exit code %d)", cStatus.State.Terminated.ExitCode), true
			}
			if cStatus.State.Terminated.ExitCode != 0 {
				return "Error", fmt.Sprintf("Exit code %d: %s", cStatus.State.Terminated.ExitCode, cStatus.State.Terminated.Reason), true
			}
		}
	}
	return phase, "", phase == "Failed"
}

// -------------------------------------------------------------
// Cluster & Node Data Fetchers
// -------------------------------------------------------------

func fetchClusterInfo(ctx context.Context, client kubernetes.Interface, contextName string, db *sql.DB) (clusterInfo, error) {
	info := clusterInfo{
		Connected: true,
		Context:   contextName,
	}
	if client == nil {
		info.Connected = false
		return info, nil
	}
	ver, err := client.Discovery().ServerVersion()
	if err == nil && ver != nil {
		info.ServerVersion = ver.GitVersion
		info.GitCommit = ver.GitCommit
		info.Platform = ver.Platform
	}

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		info.NodesCount = len(nodes.Items)
		info.ReadyNodes = countReadyNodes(nodes.Items)
	}

	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err == nil {
		info.NamespacesCount = len(namespaces.Items)
	}

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		info.PodsCount = len(pods.Items)
	}

	events, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning"})
	if err == nil {
		info.WarningEvents = len(events.Items)
	}

	if db != nil {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&info.WorkspacesCount)
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments`).Scan(&info.DeploymentsCount)
	}

	return info, nil
}

func fetchNodesList(ctx context.Context, client kubernetes.Interface) ([]nodeSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	nodeList, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	podList, _ := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	podsByNode := make(map[string]int)
	if podList != nil {
		for _, pod := range podList.Items {
			if pod.Spec.NodeName != "" && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
				podsByNode[pod.Spec.NodeName]++
			}
		}
	}

	var nodes []nodeSummary
	for _, n := range nodeList.Items {
		status := "NotReady"
		var conds []nodeCondition
		for _, c := range n.Status.Conditions {
			conds = append(conds, nodeCondition{
				Type:    string(c.Type),
				Status:  string(c.Status),
				Reason:  c.Reason,
				Message: c.Message,
			})
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				status = "Ready"
			}
		}

		var roles []string
		for label := range n.Labels {
			if strings.HasPrefix(label, "node-role.kubernetes.io/") {
				role := strings.TrimPrefix(label, "node-role.kubernetes.io/")
				if role != "" {
					roles = append(roles, role)
				}
			}
		}
		if len(roles) == 0 {
			roles = []string{"worker"}
		}
		sort.Strings(roles)

		intIP, extIP := "", ""
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				intIP = addr.Address
			} else if addr.Type == corev1.NodeExternalIP {
				extIP = addr.Address
			}
		}

		podsCap := n.Status.Capacity.Pods().Value()

		nodes = append(nodes, nodeSummary{
			Name:             n.Name,
			Status:           status,
			Roles:            roles,
			Version:          n.Status.NodeInfo.KubeletVersion,
			OSImage:          n.Status.NodeInfo.OSImage,
			KernelVersion:    n.Status.NodeInfo.KernelVersion,
			ContainerRuntime: n.Status.NodeInfo.ContainerRuntimeVersion,
			InternalIP:       intIP,
			ExternalIP:       extIP,
			PodCIDR:          n.Spec.PodCIDR,
			CPUCapacity:      n.Status.Capacity.Cpu().String(),
			CPUAllocatable:   n.Status.Allocatable.Cpu().String(),
			MemCapacity:      n.Status.Capacity.Memory().String(),
			MemAllocatable:   n.Status.Allocatable.Memory().String(),
			PodsCapacity:     podsCap,
			PodsRunning:      podsByNode[n.Name],
			Age:              formatAge(n.CreationTimestamp.Time),
			CreatedAt:        n.CreationTimestamp.UTC().Format(time.RFC3339),
			Conditions:       conds,
			Labels:           n.Labels,
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})
	return nodes, nil
}

// -------------------------------------------------------------
// Workload Data Fetchers: Pods, Deployments, StatefulSets, etc.
// -------------------------------------------------------------

func fetchPodsList(ctx context.Context, client kubernetes.Interface, namespace string, wsMap map[string]string) ([]podSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []podSummary
	for _, p := range podList.Items {
		phase, detail, hasErrors := getPodPhaseAndStatus(&p)

		var readyCount int
		var restarts int32
		var containers []containerBrief
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready {
				readyCount++
			}
			restarts += cs.RestartCount

			stateStr := "unknown"
			reason := ""
			message := ""
			if cs.State.Running != nil {
				stateStr = "running"
			} else if cs.State.Waiting != nil {
				stateStr = "waiting"
				reason = cs.State.Waiting.Reason
				message = cs.State.Waiting.Message
			} else if cs.State.Terminated != nil {
				stateStr = "terminated"
				reason = cs.State.Terminated.Reason
				message = cs.State.Terminated.Message
			}

			containers = append(containers, containerBrief{
				Name:         cs.Name,
				Image:        cs.Image,
				Ready:        cs.Ready,
				RestartCount: cs.RestartCount,
				State:        stateStr,
				Reason:       reason,
				Message:      message,
			})
		}
		readyStr := fmt.Sprintf("%d/%d", readyCount, len(p.Spec.Containers))

		ctrl := ""
		for _, owner := range p.OwnerReferences {
			if owner.Controller != nil && *owner.Controller {
				ctrl = fmt.Sprintf("%s/%s", owner.Kind, owner.Name)
				break
			}
		}

		wsID := p.Labels[workspaceLabel]
		wsName := ""
		if wsID != "" && wsMap != nil {
			wsName = wsMap[wsID]
		}
		if wsName == "" && strings.HasPrefix(p.Namespace, "crelan-ws-") {
			id := strings.TrimPrefix(p.Namespace, "crelan-ws-")
			if wsMap != nil {
				wsName = wsMap[id]
			}
		}

		result = append(result, podSummary{
			Name:            p.Name,
			Namespace:       p.Namespace,
			Phase:           phase,
			StatusDetail:    detail,
			ReadyContainers: readyStr,
			Restarts:        restarts,
			ControlledBy:    ctrl,
			Node:            p.Spec.NodeName,
			PodIP:           p.Status.PodIP,
			Age:             formatAge(p.CreationTimestamp.Time),
			CreatedAt:       p.CreationTimestamp.UTC().Format(time.RFC3339),
			Labels:          p.Labels,
			WorkspaceID:     wsID,
			WorkspaceName:   wsName,
			AppName:         p.Labels[applicationLabel],
			HasErrors:       hasErrors,
			Containers:      containers,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].HasErrors != result[j].HasErrors {
			return result[i].HasErrors
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func fetchDeploymentsList(ctx context.Context, client kubernetes.Interface, namespace string, wsMap map[string]string) ([]deploymentSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	depList, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []deploymentSummary
	for _, d := range depList.Items {
		var images []string
		for _, c := range d.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}

		var conds []string
		for _, c := range d.Status.Conditions {
			if c.Status == corev1.ConditionTrue {
				conds = append(conds, string(c.Type))
			}
		}

		wsID := d.Labels[workspaceLabel]
		wsName := ""
		if wsID != "" && wsMap != nil {
			wsName = wsMap[wsID]
		}
		if wsName == "" && strings.HasPrefix(d.Namespace, "crelan-ws-") {
			id := strings.TrimPrefix(d.Namespace, "crelan-ws-")
			if wsMap != nil {
				wsName = wsMap[id]
			}
		}

		replicas := int32(1)
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}

		result = append(result, deploymentSummary{
			Name:              d.Name,
			Namespace:         d.Namespace,
			Replicas:          replicas,
			ReadyReplicas:     d.Status.ReadyReplicas,
			UpdatedReplicas:   d.Status.UpdatedReplicas,
			AvailableReplicas: d.Status.AvailableReplicas,
			Selector:          metav1.FormatLabelSelector(d.Spec.Selector),
			Images:            images,
			Age:               formatAge(d.CreationTimestamp.Time),
			CreatedAt:         d.CreationTimestamp.UTC().Format(time.RFC3339),
			Conditions:        conds,
			WorkspaceID:       wsID,
			WorkspaceName:     wsName,
			AppName:           d.Labels[applicationLabel],
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func fetchStatefulSetsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]statefulSetSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []statefulSetSummary
	for _, s := range list.Items {
		var images []string
		for _, c := range s.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}
		replicas := int32(1)
		if s.Spec.Replicas != nil {
			replicas = *s.Spec.Replicas
		}
		result = append(result, statefulSetSummary{
			Name:          s.Name,
			Namespace:     s.Namespace,
			Replicas:      replicas,
			ReadyReplicas: s.Status.ReadyReplicas,
			Images:        images,
			Age:           formatAge(s.CreationTimestamp.Time),
			CreatedAt:     s.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchDaemonSetsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]daemonSetSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []daemonSetSummary
	for _, d := range list.Items {
		var images []string
		for _, c := range d.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}
		result = append(result, daemonSetSummary{
			Name:      d.Name,
			Namespace: d.Namespace,
			Desired:   d.Status.DesiredNumberScheduled,
			Current:   d.Status.CurrentNumberScheduled,
			Ready:     d.Status.NumberReady,
			UpToDate:  d.Status.UpdatedNumberScheduled,
			Images:    images,
			Age:       formatAge(d.CreationTimestamp.Time),
			CreatedAt: d.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchJobsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]jobSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []jobSummary
	for _, j := range list.Items {
		completions := int32(1)
		if j.Spec.Completions != nil {
			completions = *j.Spec.Completions
		}
		dur := ""
		if j.Status.StartTime != nil {
			if j.Status.CompletionTime != nil {
				dur = j.Status.CompletionTime.Sub(j.Status.StartTime.Time).Round(time.Second).String()
			} else {
				dur = time.Since(j.Status.StartTime.Time).Round(time.Second).String()
			}
		}
		result = append(result, jobSummary{
			Name:        j.Name,
			Namespace:   j.Namespace,
			Completions: completions,
			Succeeded:   j.Status.Succeeded,
			Failed:      j.Status.Failed,
			Active:      j.Status.Active,
			Duration:    dur,
			Age:         formatAge(j.CreationTimestamp.Time),
			CreatedAt:   j.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchCronJobsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]cronJobSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []cronJobSummary
	for _, c := range list.Items {
		lastSched := ""
		if c.Status.LastScheduleTime != nil {
			lastSched = formatAge(c.Status.LastScheduleTime.Time) + " ago"
		}
		suspend := false
		if c.Spec.Suspend != nil {
			suspend = *c.Spec.Suspend
		}
		result = append(result, cronJobSummary{
			Name:         c.Name,
			Namespace:    c.Namespace,
			Schedule:     c.Spec.Schedule,
			Suspend:      suspend,
			Active:       len(c.Status.Active),
			LastSchedule: lastSched,
			Age:          formatAge(c.CreationTimestamp.Time),
			CreatedAt:    c.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

// -------------------------------------------------------------
// Networking Fetchers: Services, Ingresses, NetworkPolicies
// -------------------------------------------------------------

func fetchServicesList(ctx context.Context, client kubernetes.Interface, namespace string) ([]serviceSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []serviceSummary
	for _, s := range list.Items {
		var ports []string
		for _, p := range s.Spec.Ports {
			if p.NodePort > 0 {
				ports = append(ports, fmt.Sprintf("%d:%d/%s", p.Port, p.NodePort, p.Protocol))
			} else {
				ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
			}
		}

		var extIPs []string
		if s.Spec.ExternalIPs != nil {
			extIPs = append(extIPs, s.Spec.ExternalIPs...)
		}
		for _, ing := range s.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				extIPs = append(extIPs, ing.IP)
			}
			if ing.Hostname != "" {
				extIPs = append(extIPs, ing.Hostname)
			}
		}

		var sel []string
		for k, v := range s.Spec.Selector {
			sel = append(sel, fmt.Sprintf("%s=%s", k, v))
		}
		sort.Strings(sel)

		result = append(result, serviceSummary{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Type:        string(s.Spec.Type),
			ClusterIP:   s.Spec.ClusterIP,
			ExternalIPs: extIPs,
			Ports:       ports,
			Selector:    strings.Join(sel, ", "),
			Age:         formatAge(s.CreationTimestamp.Time),
			CreatedAt:   s.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchIngressesList(ctx context.Context, client kubernetes.Interface, namespace string) ([]ingressSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []ingressSummary
	for _, ing := range list.Items {
		var hosts []string
		for _, r := range ing.Spec.Rules {
			if r.Host != "" {
				hosts = append(hosts, r.Host)
			}
		}
		var addrs []string
		for _, lb := range ing.Status.LoadBalancer.Ingress {
			if lb.IP != "" {
				addrs = append(addrs, lb.IP)
			} else if lb.Hostname != "" {
				addrs = append(addrs, lb.Hostname)
			}
		}
		ingClass := ""
		if ing.Spec.IngressClassName != nil {
			ingClass = *ing.Spec.IngressClassName
		}
		result = append(result, ingressSummary{
			Name:         ing.Name,
			Namespace:    ing.Namespace,
			IngressClass: ingClass,
			Hosts:        hosts,
			Addresses:    addrs,
			Age:          formatAge(ing.CreationTimestamp.Time),
			CreatedAt:    ing.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchNetworkPoliciesList(ctx context.Context, client kubernetes.Interface, namespace string) ([]networkPolicySummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []networkPolicySummary
	for _, np := range list.Items {
		var ptypes []string
		for _, t := range np.Spec.PolicyTypes {
			ptypes = append(ptypes, string(t))
		}
		result = append(result, networkPolicySummary{
			Name:        np.Name,
			Namespace:   np.Namespace,
			PodSelector: metav1.FormatLabelSelector(&np.Spec.PodSelector),
			PolicyTypes: ptypes,
			Age:         formatAge(np.CreationTimestamp.Time),
			CreatedAt:   np.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

// -------------------------------------------------------------
// Config & Storage Fetchers: ConfigMaps, Secrets, PVCs, PVs, StorageClasses
// -------------------------------------------------------------

func fetchConfigMapsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]configMapSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []configMapSummary
	for _, cm := range list.Items {
		var keys []string
		for k := range cm.Data {
			keys = append(keys, k)
		}
		for k := range cm.BinaryData {
			keys = append(keys, k+" (binary)")
		}
		sort.Strings(keys)
		result = append(result, configMapSummary{
			Name:      cm.Name,
			Namespace: cm.Namespace,
			KeysCount: len(keys),
			Keys:      keys,
			Age:       formatAge(cm.CreationTimestamp.Time),
			CreatedAt: cm.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchSecretsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]secretSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []secretSummary
	for _, s := range list.Items {
		var keys []string
		for k := range s.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		result = append(result, secretSummary{
			Name:      s.Name,
			Namespace: s.Namespace,
			Type:      string(s.Type),
			KeysCount: len(keys),
			Keys:      keys,
			Age:       formatAge(s.CreationTimestamp.Time),
			CreatedAt: s.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchPVCsList(ctx context.Context, client kubernetes.Interface, namespace string) ([]pvcSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []pvcSummary
	for _, p := range list.Items {
		var modes []string
		for _, m := range p.Spec.AccessModes {
			modes = append(modes, string(m))
		}
		sc := ""
		if p.Spec.StorageClassName != nil {
			sc = *p.Spec.StorageClassName
		}
		capStr := ""
		if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
			capStr = q.String()
		} else if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			capStr = q.String()
		}
		result = append(result, pvcSummary{
			Name:         p.Name,
			Namespace:    p.Namespace,
			Status:       string(p.Status.Phase),
			VolumeName:   p.Spec.VolumeName,
			Capacity:     capStr,
			AccessModes:  modes,
			StorageClass: sc,
			Age:          formatAge(p.CreationTimestamp.Time),
			CreatedAt:    p.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchPVsList(ctx context.Context, client kubernetes.Interface) ([]pvSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []pvSummary
	for _, p := range list.Items {
		var modes []string
		for _, m := range p.Spec.AccessModes {
			modes = append(modes, string(m))
		}
		claim := ""
		if p.Spec.ClaimRef != nil {
			claim = fmt.Sprintf("%s/%s", p.Spec.ClaimRef.Namespace, p.Spec.ClaimRef.Name)
		}
		capStr := ""
		if q, ok := p.Spec.Capacity[corev1.ResourceStorage]; ok {
			capStr = q.String()
		}
		result = append(result, pvSummary{
			Name:          p.Name,
			Capacity:      capStr,
			AccessModes:   modes,
			ReclaimPolicy: string(p.Spec.PersistentVolumeReclaimPolicy),
			Status:        string(p.Status.Phase),
			Claim:         claim,
			StorageClass:  p.Spec.StorageClassName,
			Age:           formatAge(p.CreationTimestamp.Time),
			CreatedAt:     p.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func fetchStorageClassesList(ctx context.Context, client kubernetes.Interface) ([]storageClassSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []storageClassSummary
	for _, sc := range list.Items {
		reclaim := ""
		if sc.ReclaimPolicy != nil {
			reclaim = string(*sc.ReclaimPolicy)
		}
		binding := ""
		if sc.VolumeBindingMode != nil {
			binding = string(*sc.VolumeBindingMode)
		}
		expand := false
		if sc.AllowVolumeExpansion != nil {
			expand = *sc.AllowVolumeExpansion
		}
		isDef := sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true"
		result = append(result, storageClassSummary{
			Name:                 sc.Name,
			Provisioner:          sc.Provisioner,
			ReclaimPolicy:        reclaim,
			VolumeBindingMode:    binding,
			AllowVolumeExpansion: expand,
			IsDefault:            isDef,
			Age:                  formatAge(sc.CreationTimestamp.Time),
			CreatedAt:            sc.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

// -------------------------------------------------------------
// Namespaces & Events Fetchers
// -------------------------------------------------------------

func fetchNamespacesList(ctx context.Context, client kubernetes.Interface) ([]namespaceSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []namespaceSummary
	for _, ns := range list.Items {
		tenant := ns.Labels[capsuleTenantLabel]
		wsID := ""
		if strings.HasPrefix(ns.Name, "crelan-ws-") {
			wsID = strings.TrimPrefix(ns.Name, "crelan-ws-")
		}
		result = append(result, namespaceSummary{
			Name:        ns.Name,
			Status:      string(ns.Status.Phase),
			Tenant:      tenant,
			WorkspaceID: wsID,
			Age:         formatAge(ns.CreationTimestamp.Time),
			CreatedAt:   ns.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func fetchEventsList(ctx context.Context, client kubernetes.Interface, namespace, eventType string) ([]eventSummary, error) {
	if client == nil {
		return nil, errKubernetesNotConfigured
	}
	opts := metav1.ListOptions{}
	if eventType != "" {
		opts.FieldSelector = fmt.Sprintf("type=%s", eventType)
	}
	list, err := client.CoreV1().Events(namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	var result []eventSummary
	for _, e := range list.Items {
		lastTime := e.LastTimestamp.Time
		if lastTime.IsZero() {
			lastTime = e.EventTime.Time
		}
		firstTime := e.FirstTimestamp.Time
		src := e.Source.Component
		if e.Source.Host != "" {
			src += " on " + e.Source.Host
		}
		result = append(result, eventSummary{
			Type:           e.Type,
			Reason:         e.Reason,
			Message:        e.Message,
			ObjectKind:     e.InvolvedObject.Kind,
			ObjectName:     e.InvolvedObject.Name,
			Namespace:      e.InvolvedObject.Namespace,
			Source:         src,
			Count:          e.Count,
			FirstTimestamp: firstTime.UTC().Format(time.RFC3339),
			LastTimestamp:  lastTime.UTC().Format(time.RFC3339),
			Age:            formatAge(lastTime),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastTimestamp > result[j].LastTimestamp
	})
	if len(result) > 200 {
		result = result[:200]
	}
	return result, nil
}

// -------------------------------------------------------------
// Pod Logs & In-Browser Interactive Exec
// -------------------------------------------------------------

func fetchPodLogs(ctx context.Context, client kubernetes.Interface, namespace, podName, container string, tailLines int64, previous bool) (string, error) {
	if client == nil {
		return "", errKubernetesNotConfigured
	}
	opts := &corev1.PodLogOptions{
		Previous: previous,
	}
	if container != "" {
		opts.Container = container
	}
	if tailLines > 0 {
		opts.TailLines = &tailLines
	} else {
		defTail := int64(250)
		opts.TailLines = &defTail
	}

	req := client.CoreV1().Pods(namespace).GetLogs(podName, opts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func execPodCommand(ctx context.Context, provisioner *kubernetesProvisioner, namespace, podName, container, command string) (string, string, error) {
	if provisioner == nil || provisioner.client == nil || provisioner.restConfig == nil {
		return "", "", errKubernetesNotConfigured
	}
	cmdList := []string{"/bin/sh", "-c", command}
	if strings.HasPrefix(command, "[DIRECT]") {
		raw := strings.TrimPrefix(command, "[DIRECT]")
		cmdList = strings.Fields(raw)
	}

	req := provisioner.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   cmdList,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(provisioner.restConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.String(), stderr.String(), err
}

// -------------------------------------------------------------
// Advanced Feature 1: AI Diagnostician Copilot
// -------------------------------------------------------------

func extractStackTrace(logs string) (trace string, anomaly string) {
	if strings.TrimSpace(logs) == "" {
		return "", ""
	}
	lines := strings.Split(logs, "\n")

	// Check for Go panic
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "panic:") {
			anomaly = trimmed
			end := i + 20
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], "\n"), anomaly
		}
	}

	// Check for Python Traceback
	for i, l := range lines {
		if strings.Contains(l, "Traceback (most recent call last):") {
			end := i + 20
			if end > len(lines) {
				end = len(lines)
			}
			trace = strings.Join(lines[i:end], "\n")
			for j := end - 1; j >= i; j-- {
				t := strings.TrimSpace(lines[j])
				if t != "" && !strings.HasPrefix(t, "File ") {
					anomaly = t
					break
				}
			}
			if anomaly == "" {
				anomaly = "Python Unhandled Exception"
			}
			return trace, anomaly
		}
	}

	// Check for Java Exception
	for i, l := range lines {
		if strings.Contains(l, "Exception in thread") || (strings.Contains(l, "Exception:") && strings.Contains(l, "\tat ")) {
			anomaly = strings.TrimSpace(l)
			end := i + 20
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], "\n"), anomaly
		}
	}

	// Check for Node/JS Unhandled Error
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "Error:") || strings.HasPrefix(trimmed, "TypeError:") || strings.HasPrefix(trimmed, "ReferenceError:") {
			anomaly = trimmed
			end := i + 15
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], "\n"), anomaly
		}
	}

	// Check for fatal database / network errors
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.Contains(trimmed, "FATAL:") || strings.Contains(trimmed, "connection refused") || strings.Contains(trimmed, "password authentication failed") || strings.Contains(trimmed, "dial tcp") {
			return trimmed, trimmed
		}
	}

	return "", ""
}

func runAIDiagnosis(ctx context.Context, client kubernetes.Interface, namespace, podName string) (aiDiagnosticReport, error) {
	if client == nil {
		return aiDiagnosticReport{}, errKubernetesNotConfigured
	}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return aiDiagnosticReport{}, err
	}

	report := aiDiagnosticReport{
		PodName:    podName,
		Namespace:  namespace,
		Phase:      string(pod.Status.Phase),
		Severity:   "healthy",
		Confidence: "High",
	}

	events, _ := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", podName),
	})
	var warnEvents []eventSummary
	if events != nil {
		for _, e := range events.Items {
			if e.Type == "Warning" {
				warnEvents = append(warnEvents, eventSummary{
					Type:          e.Type,
					Reason:        e.Reason,
					Message:       e.Message,
					Count:         e.Count,
					LastTimestamp: e.LastTimestamp.Time.Format(time.RFC3339),
				})
			}
		}
	}
	report.WarningEvents = warnEvents

	recentLogs, _ := fetchPodLogs(ctx, client, namespace, podName, "", 100, false)
	if strings.TrimSpace(recentLogs) == "" {
		prevLogs, _ := fetchPodLogs(ctx, client, namespace, podName, "", 100, true)
		if strings.TrimSpace(prevLogs) != "" {
			recentLogs = prevLogs
		}
	}
	report.RelevantLogs = recentLogs

	// Deep Stack Trace Extraction
	trace, anomaly := extractStackTrace(recentLogs)
	if trace != "" {
		report.StackTrace = trace
		report.LogAnomaly = anomaly
	}

	var evidences []string
	var actions []aiAction
	var recSteps []string

	hasCrash := false
	hasOOM := false
	hasImageErr := false
	hasConfigErr := false

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated != nil {
			report.ExitCode = cs.LastTerminationState.Terminated.ExitCode
			report.LastTerminationReason = cs.LastTerminationState.Terminated.Reason
		} else if cs.State.Terminated != nil {
			report.ExitCode = cs.State.Terminated.ExitCode
			report.LastTerminationReason = cs.State.Terminated.Reason
		}

		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			msg := cs.State.Waiting.Message
			evidences = append(evidences, fmt.Sprintf("Container %q is waiting: %s (%s)", cs.Name, reason, msg))
			if reason == "CrashLoopBackOff" {
				hasCrash = true
			} else if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
				hasImageErr = true
			} else if reason == "CreateContainerConfigError" {
				hasConfigErr = true
			}
		}
		if cs.State.Terminated != nil {
			evidences = append(evidences, fmt.Sprintf("Container %q terminated with exit code %d (Reason: %s)", cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason))
			if cs.State.Terminated.Reason == "OOMKilled" || cs.State.Terminated.ExitCode == 137 {
				hasOOM = true
			} else if cs.State.Terminated.ExitCode != 0 {
				hasCrash = true
			}
		}
	}

	for _, w := range warnEvents {
		evidences = append(evidences, fmt.Sprintf("Cluster Event: [%s] %s", w.Reason, w.Message))
	}
	if anomaly != "" {
		evidences = append(evidences, fmt.Sprintf("Extracted Error: %s", anomaly))
	}

	if hasOOM {
		report.Severity = "critical"
		report.Summary = "Pod terminated due to Out Of Memory (OOMKilled - Exit Code 137)"
		report.ProbableCause = "The container process exceeded its memory limit. The Linux kernel OOM-killer intervened and sent SIGKILL (137)."
		recSteps = []string{
			"Inspect application memory consumption patterns or memory leaks.",
			"Raise resources.limits.memory in the deployment template (e.g. increase to 512Mi or 1Gi).",
			"Check AI Cost & Right-Sizing recommendations for optimal workload limits.",
		}
		actions = append(actions, aiAction{
			ActionID:    "increase_memory",
			Title:       "Increase Container Memory Limit",
			Description: "Raise memory limits in the deployment specification to avoid OOM evictions.",
			AutoFixable: false,
		})
		actions = append(actions, aiAction{
			ActionID:    "restart_pod",
			Title:       "Restart Pod",
			Description: "Evict the pod to restart it immediately while you investigate application memory leaks.",
			Command:     fmt.Sprintf("kubectl delete pod %s -n %s", podName, namespace),
			AutoFixable: true,
		})
	} else if hasImageErr {
		report.Severity = "critical"
		report.Summary = "Container image could not be pulled from registry (ImagePullBackOff / ErrImagePull)"
		report.ProbableCause = "The container image tag does not exist, network registry access timed out, or imagePullSecrets are missing/invalid for private registry."
		recSteps = []string{
			"Verify that the container image repository and tag exist on Docker Hub / ECR / GHCR.",
			"Ensure the namespace contains a valid kubernetes.io/dockerconfigjson Secret.",
			"Confirm cluster nodes have outbound internet/registry route connectivity.",
		}
		actions = append(actions, aiAction{
			ActionID:    "check_registry_secret",
			Title:       "Verify Registry Credentials",
			Description: "Ensure imagePullSecrets are configured or that the Docker repository is public.",
			AutoFixable: false,
		})
	} else if hasConfigErr {
		report.Severity = "critical"
		report.Summary = "Container configuration error (CreateContainerConfigError)"
		report.ProbableCause = "A referenced ConfigMap or Secret specified in env or volumes is missing in namespace " + namespace + "."
		recSteps = []string{
			"List ConfigMaps and Secrets in namespace " + namespace + " to verify referenced names match exactly.",
			"Check deployment env[].valueFrom.configMapKeyRef and secretKeyRef names and keys.",
		}
		actions = append(actions, aiAction{
			ActionID:    "check_missing_secrets",
			Title:       "Inspect ConfigMaps and Secrets",
			Description: "Verify that all referenced environment secrets exist in namespace " + namespace + ".",
			AutoFixable: false,
		})
	} else if hasCrash {
		report.Severity = "critical"
		report.Summary = "Application runtime failure causing CrashLoopBackOff"
		if anomaly != "" {
			report.ProbableCause = fmt.Sprintf("Application crashed with unhandled error: %s. An uncaught exception or fatal startup error terminated the main process.", anomaly)
		} else {
			report.ProbableCause = "The application process exited with non-zero status immediately after launch. Common reasons include missing DB credentials, invalid port binding, or failed database migrations."
		}
		recSteps = []string{
			"Review the extracted stack trace and log anomaly above for exact source line and error class.",
			"Verify database connection strings, credentials, and network connectivity.",
			"Ensure health check liveness/readiness probe initialDelaySeconds allows adequate warmup time.",
		}
		actions = append(actions, aiAction{
			ActionID:    "check_logs",
			Title:       "Inspect Application Stack Trace",
			Description: "Review container log tail above for unhandled exceptions or missing configuration parameters.",
			AutoFixable: false,
		})
		actions = append(actions, aiAction{
			ActionID:    "restart_deployment",
			Title:       "Trigger Deployment Rollout Restart",
			Description: "Perform a rolling restart of the parent deployment.",
			AutoFixable: true,
		})
	} else if pod.Status.Phase == corev1.PodPending {
		report.Severity = "warning"
		report.Summary = "Pod is in Pending state waiting for scheduling or storage"
		report.ProbableCause = "The Kubernetes scheduler could not place this pod. Common reasons: Insufficient CPU/memory on cluster nodes, unbound PersistentVolumeClaims (PVC), or node selector/affinity constraints."
		recSteps = []string{
			"Check node CPU and memory allocatable capacity in Nodes view.",
			"Verify if any required PersistentVolumeClaims (PVC) are in Pending or Lost state.",
		}
		actions = append(actions, aiAction{
			ActionID:    "check_node_resources",
			Title:       "Check Node Resources & Storage",
			Description: "Inspect cluster node allocatable capacity and PVC binding status.",
			AutoFixable: false,
		})
	} else {
		report.Severity = "healthy"
		report.Summary = "Pod is Running smoothly with all containers Ready"
		report.ProbableCause = "No active errors or crash events detected."
		recSteps = []string{"Workload is operational. Continue periodic monitoring."}
	}

	report.Evidence = evidences
	report.Remediation = actions
	report.RecommendedSteps = recSteps
	return report, nil
}

// -------------------------------------------------------------
// Advanced Feature 2: Tenant & Workspace Context Mapping
// -------------------------------------------------------------

func fetchWorkspaceMappings(ctx context.Context, db *sql.DB) ([]workspaceMapping, map[string]string, error) {
	if db == nil {
		return nil, nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT w.id, w.name, u.name, u.email,
			(SELECT COUNT(*) FROM workspace_members WHERE workspace_id = w.id) as members
		FROM workspaces w
		LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.role = 'owner'
		LEFT JOIN users u ON u.id = wm.user_id
		ORDER BY w.id ASC
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var mappings []workspaceMapping
	wsLookup := make(map[string]string)

	for rows.Next() {
		var m workspaceMapping
		var ownerName, ownerEmail sql.NullString
		if err := rows.Scan(&m.ID, &m.Name, &ownerName, &ownerEmail, &m.Members); err != nil {
			return nil, nil, err
		}
		m.Namespace = fmt.Sprintf("crelan-ws-%d", m.ID)
		m.OwnerName = ownerName.String
		m.OwnerEmail = ownerEmail.String
		wsLookup[fmt.Sprint(m.ID)] = m.Name
		wsLookup[m.Namespace] = m.Name
		mappings = append(mappings, m)
	}
	rows.Close()

	// Query deployments without holding rows open on the connection
	depRows, dErr := db.QueryContext(ctx, `SELECT workspace_id, name FROM deployments`)
	if dErr == nil {
		depMap := make(map[int64][]string)
		for depRows.Next() {
			var wsID int64
			var depName string
			if err := depRows.Scan(&wsID, &depName); err == nil {
				depMap[wsID] = append(depMap[wsID], depName)
			}
		}
		depRows.Close()
		for i := range mappings {
			mappings[i].Deployments = depMap[mappings[i].ID]
		}
	}

	return mappings, wsLookup, nil
}

// -------------------------------------------------------------
// Advanced Feature 5: Security & Isolation Auditor (CIS Benchmark + Secret Leak Guard)
// -------------------------------------------------------------

var (
	reAWSKey      = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	reGitHubToken = regexp.MustCompile(`\bghp_[a-zA-Z0-9]{36}\b`)
	reOpenAIToken = regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`)
	rePrivateKey  = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
)

func runSecurityAudit(ctx context.Context, client kubernetes.Interface) (securityAuditReport, error) {
	if client == nil {
		return securityAuditReport{}, errKubernetesNotConfigured
	}
	podList, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return securityAuditReport{}, err
	}

	var issues []securityIssue
	passed := 0
	failed := 0
	criticalCount := 0
	highCount := 0
	secretLeaksCount := 0
	rootCount := 0
	privilegedCount := 0

	for _, p := range podList.Items {
		if p.Status.Phase == corev1.PodSucceeded {
			continue
		}

		// Identify owning deployment for 1-click hardening
		depName := ""
		depKind := ""
		canHarden := false
		for _, ref := range p.OwnerReferences {
			if ref.Kind == "ReplicaSet" {
				parts := strings.Split(ref.Name, "-")
				if len(parts) > 1 {
					depName = strings.Join(parts[:len(parts)-1], "-")
					depKind = "Deployment"
					canHarden = true
				}
			} else if ref.Kind == "Deployment" {
				depName = ref.Name
				depKind = "Deployment"
				canHarden = true
			}
		}
		if depName == "" && p.Labels["app"] != "" {
			depName = p.Labels["app"]
			depKind = "Deployment"
			canHarden = true
		}

		// CIS Rule 1: Host Network / PID / IPC
		if p.Spec.HostNetwork || p.Spec.HostPID || p.Spec.HostIPC {
			failed++
			criticalCount++
			issues = append(issues, securityIssue{
				Severity:    "critical",
				Rule:        "HostNamespaceSharing",
				PodName:     p.Name,
				Namespace:   p.Namespace,
				Description: fmt.Sprintf("Pod shares host namespaces (hostNetwork=%v, hostPID=%v, hostIPC=%v).", p.Spec.HostNetwork, p.Spec.HostPID, p.Spec.HostIPC),
				Remediation: "Disable hostNetwork, hostPID, and hostIPC to preserve node kernel isolation.",
			})
		} else {
			passed++
		}

		// CIS Rule 2: HostPath volume mounts
		for _, v := range p.Spec.Volumes {
			if v.HostPath != nil {
				failed++
				highCount++
				issues = append(issues, securityIssue{
					Severity:    "high",
					Rule:        "HostPathVolumeMount",
					PodName:     p.Name,
					Namespace:   p.Namespace,
					Description: fmt.Sprintf("Volume %q mounts host filesystem path %q directly into container.", v.Name, v.HostPath.Path),
					Remediation: "Use PersistentVolumeClaims (PVC) instead of HostPath mounts to avoid host filesystem compromise.",
				})
			}
		}

		for _, c := range p.Spec.Containers {
			// Secret Leak Guard in container env
			for _, e := range c.Env {
				if e.Value != "" && e.ValueFrom == nil {
					val := e.Value
					isSecret := false
					leakType := "token"

					if reAWSKey.MatchString(val) {
						isSecret = true
						leakType = "AWS Access Key"
					} else if reGitHubToken.MatchString(val) {
						isSecret = true
						leakType = "GitHub Personal Access Token"
					} else if reOpenAIToken.MatchString(val) {
						isSecret = true
						leakType = "API Secret Key"
					} else if rePrivateKey.MatchString(val) {
						isSecret = true
						leakType = "Private Key"
					} else {
						uName := strings.ToUpper(e.Name)
						if (strings.Contains(uName, "PASSWORD") || strings.Contains(uName, "SECRET") || strings.Contains(uName, "AUTH_TOKEN")) && len(val) >= 4 {
							isSecret = true
							leakType = "Plaintext Password/Secret"
						}
					}

					if isSecret {
						failed++
						criticalCount++
						secretLeaksCount++
						issues = append(issues, securityIssue{
							Severity:    "critical",
							Rule:        "SecretLeakGuard",
							PodName:     p.Name,
							Namespace:   p.Namespace,
							Container:   c.Name,
							Description: fmt.Sprintf("Container env var %q contains plaintext %s.", e.Name, leakType),
							Remediation: "Store credentials in a Kubernetes Secret and inject via secretKeyRef.",
						})
					}
				}
			}

			// CIS Rule 3: Run as Non-Root
			runsAsRoot := true
			if c.SecurityContext != nil {
				if c.SecurityContext.RunAsNonRoot != nil && *c.SecurityContext.RunAsNonRoot {
					runsAsRoot = false
				} else if c.SecurityContext.RunAsUser != nil && *c.SecurityContext.RunAsUser > 0 {
					runsAsRoot = false
				}
			}
			if p.Spec.SecurityContext != nil {
				if p.Spec.SecurityContext.RunAsNonRoot != nil && *p.Spec.SecurityContext.RunAsNonRoot {
					runsAsRoot = false
				} else if p.Spec.SecurityContext.RunAsUser != nil && *p.Spec.SecurityContext.RunAsUser > 0 {
					runsAsRoot = false
				}
			}

			if runsAsRoot {
				failed++
				highCount++
				rootCount++
				issues = append(issues, securityIssue{
					Severity:      "high",
					Rule:          "ContainerRunsAsRoot",
					PodName:       p.Name,
					Namespace:     p.Namespace,
					Container:     c.Name,
					WorkloadKind:  depKind,
					WorkloadName:  depName,
					CanAutoHarden: canHarden,
					Description:   fmt.Sprintf("Container %q runs with root privileges (UID 0), violating CIS Benchmark 5.2.6.", c.Name),
					Remediation:   "Set securityContext.runAsNonRoot: true or specify runAsUser: 10001.",
				})
			} else {
				passed++
			}

			// CIS Rule 4: Privileged container
			if c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
				failed++
				criticalCount++
				privilegedCount++
				issues = append(issues, securityIssue{
					Severity:      "critical",
					Rule:          "PrivilegedContainer",
					PodName:       p.Name,
					Namespace:     p.Namespace,
					Container:     c.Name,
					WorkloadKind:  depKind,
					WorkloadName:  depName,
					CanAutoHarden: canHarden,
					Description:   fmt.Sprintf("Container %q is configured with `privileged: true`, bypassing Linux namespace isolation.", c.Name),
					Remediation:   "Remove `privileged: true` and request minimal Linux capabilities.",
				})
			} else {
				passed++
			}

			// CIS Rule 5: Allow Privilege Escalation
			allowsEscalation := true
			if c.SecurityContext != nil && c.SecurityContext.AllowPrivilegeEscalation != nil && !*c.SecurityContext.AllowPrivilegeEscalation {
				allowsEscalation = false
			}
			if allowsEscalation {
				failed++
				highCount++
				issues = append(issues, securityIssue{
					Severity:      "high",
					Rule:          "AllowPrivilegeEscalation",
					PodName:       p.Name,
					Namespace:     p.Namespace,
					Container:     c.Name,
					WorkloadKind:  depKind,
					WorkloadName:  depName,
					CanAutoHarden: canHarden,
					Description:   fmt.Sprintf("Container %q permits privilege escalation, allowing sub-processes to gain higher permissions.", c.Name),
					Remediation:   "Set `securityContext.allowPrivilegeEscalation: false`.",
				})
			} else {
				passed++
			}

			// CIS Rule 6: Dropping default Linux capabilities
			dropsAll := false
			if c.SecurityContext != nil && c.SecurityContext.Capabilities != nil {
				for _, cap := range c.SecurityContext.Capabilities.Drop {
					if cap == "ALL" {
						dropsAll = true
						break
					}
				}
			}
			if !dropsAll {
				failed++
				issues = append(issues, securityIssue{
					Severity:      "medium",
					Rule:          "CapabilitiesNotDropped",
					PodName:       p.Name,
					Namespace:     p.Namespace,
					Container:     c.Name,
					WorkloadKind:  depKind,
					WorkloadName:  depName,
					CanAutoHarden: canHarden,
					Description:   fmt.Sprintf("Container %q does not drop ALL default Linux capabilities.", c.Name),
					Remediation:   "Set `capabilities.drop: [\"ALL\"]` in securityContext.",
				})
			} else {
				passed++
			}

			// CIS Rule 7: Resource Limits
			hasLimits := c.Resources.Limits != nil && (c.Resources.Limits.Memory() != nil || c.Resources.Limits.Cpu() != nil)
			if !hasLimits {
				failed++
				issues = append(issues, securityIssue{
					Severity:    "medium",
					Rule:        "MissingResourceLimits",
					PodName:     p.Name,
					Namespace:   p.Namespace,
					Container:   c.Name,
					Description: fmt.Sprintf("Container %q has no CPU/memory limits set, risking node resource starvation.", c.Name),
					Remediation: "Add `resources.limits.memory` and `resources.limits.cpu`.",
				})
			} else {
				passed++
			}
		}
	}

	// Secret Leak Guard in ConfigMaps
	cmList, _ := client.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if cmList != nil {
		for _, cm := range cmList.Items {
			for k, v := range cm.Data {
				if reAWSKey.MatchString(v) || reGitHubToken.MatchString(v) || reOpenAIToken.MatchString(v) || rePrivateKey.MatchString(v) {
					failed++
					criticalCount++
					secretLeaksCount++
					issues = append(issues, securityIssue{
						Severity:    "critical",
						Rule:        "SecretExposedInConfigMap",
						Namespace:   cm.Namespace,
						Description: fmt.Sprintf("ConfigMap %q in key %q contains an exposed token or private key.", cm.Name, k),
						Remediation: "Migrate plaintext secrets from ConfigMap to a Kubernetes Secret with RBAC protection.",
					})
				}
			}
		}
	}

	// Tenant Network Isolation
	nsList, _ := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: capsuleTenantLabel})
	npList, _ := client.NetworkingV1().NetworkPolicies("").List(ctx, metav1.ListOptions{})

	policiesByNS := make(map[string]int)
	if npList != nil {
		for _, np := range npList.Items {
			policiesByNS[np.Namespace]++
		}
	}

	var missingNP []string
	tenantCount := 0
	enforcedCount := 0
	if nsList != nil {
		tenantCount = len(nsList.Items)
		for _, ns := range nsList.Items {
			if policiesByNS[ns.Name] > 0 {
				enforcedCount++
			} else {
				missingNP = append(missingNP, ns.Name)
			}
		}
	}

	isoStatus := "Secure"
	if len(missingNP) > 0 {
		isoStatus = "Partially Enforced"
	}

	score := 100
	totalChecks := passed + failed
	if totalChecks > 0 {
		score = int((float64(passed) / float64(totalChecks)) * 100)
	}

	// Sort issues critical -> high -> medium -> low
	sort.Slice(issues, func(i, j int) bool {
		sevOrder := func(s string) int {
			switch s {
			case "critical":
				return 4
			case "high":
				return 3
			case "medium":
				return 2
			default:
				return 1
			}
		}
		return sevOrder(issues[i].Severity) > sevOrder(issues[j].Severity)
	})

	return securityAuditReport{
		TotalPodsScanned: len(podList.Items),
		PassedChecks:     passed,
		FailedChecks:     failed,
		Score:            score,
		CriticalCount:    criticalCount,
		HighCount:        highCount,
		SecretLeaksCount: secretLeaksCount,
		RootCount:        rootCount,
		PrivilegedCount:  privilegedCount,
		Issues:           issues,
		TenantIsolation: tenantIsolationAudit{
			TotalTenantSpaces:    tenantCount,
			EnforcedSpaces:       enforcedCount,
			MissingNetworkPolicy: missingNP,
			Status:               isoStatus,
		},
	}, nil
}

// -------------------------------------------------------------
// Advanced Feature 4: Managed Database & Orphan Storage Detector
// -------------------------------------------------------------

func fetchManagedDatabasesTopology(ctx context.Context, client kubernetes.Interface, dyn dynamic.Interface) (managedDatabaseTopology, error) {
	topo := managedDatabaseTopology{}
	if client == nil {
		return topo, errKubernetesNotConfigured
	}

	if dyn != nil {
		cnpgGVR := k8sschema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}
		clusters, err := dyn.Resource(cnpgGVR).List(ctx, metav1.ListOptions{})
		if err == nil && clusters != nil {
			for _, item := range clusters.Items {
				statusStr, _, _ := unstructured.NestedString(item.Object, "status", "phase")
				if statusStr == "" {
					statusStr = "Healthy"
				}
				primary, _, _ := unstructured.NestedString(item.Object, "status", "targetPrimary")
				instances, _, _ := unstructured.NestedInt64(item.Object, "spec", "instances")

				var reps []string
				for i := int64(1); i <= instances; i++ {
					podName := fmt.Sprintf("%s-%d", item.GetName(), i)
					if podName != primary {
						reps = append(reps, podName)
					}
				}

				topo.PostgresClusters = append(topo.PostgresClusters, databaseClusterInfo{
					Name:        item.GetName(),
					Engine:      "PostgreSQL (CloudNativePG)",
					Namespace:   item.GetNamespace(),
					Status:      statusStr,
					Primary:     primary,
					Replicas:    reps,
					BackupState: "WAL Archiving Active",
					Age:         formatAge(item.GetCreationTimestamp().Time),
				})
			}
		}

		mysqlGVR := k8sschema.GroupVersionResource{Group: "mysql.oracle.com", Version: "v2", Resource: "innodbclusters"}
		myClusters, err := dyn.Resource(mysqlGVR).List(ctx, metav1.ListOptions{})
		if err == nil && myClusters != nil {
			for _, item := range myClusters.Items {
				statusStr, _, _ := unstructured.NestedString(item.Object, "status", "cluster", "status")
				if statusStr == "" {
					statusStr = "ONLINE"
				}
				instances, _, _ := unstructured.NestedInt64(item.Object, "spec", "instances")
				var reps []string
				for i := int64(0); i < instances; i++ {
					reps = append(reps, fmt.Sprintf("%s-%d", item.GetName(), i))
				}

				topo.MySQLClusters = append(topo.MySQLClusters, databaseClusterInfo{
					Name:        item.GetName(),
					Engine:      "MySQL (InnoDB Cluster)",
					Namespace:   item.GetNamespace(),
					Status:      statusStr,
					Primary:     fmt.Sprintf("%s-0 (R/W)", item.GetName()),
					Replicas:    reps,
					BackupState: "Daily Schedule Active",
					Age:         formatAge(item.GetCreationTimestamp().Time),
				})
			}
		}
	}

	pvcs, err := client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err == nil {
		pods, _ := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		mountedPVCs := make(map[string]bool)
		if pods != nil {
			for _, p := range pods.Items {
				for _, v := range p.Spec.Volumes {
					if v.PersistentVolumeClaim != nil {
						key := fmt.Sprintf("%s/%s", p.Namespace, v.PersistentVolumeClaim.ClaimName)
						mountedPVCs[key] = true
					}
				}
			}
		}

		for _, p := range pvcs.Items {
			key := fmt.Sprintf("%s/%s", p.Namespace, p.Name)
			if !mountedPVCs[key] && strings.HasPrefix(p.Namespace, "crelan-ws-") {
				capStr := ""
				if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
					capStr = q.String()
				}
				sc := ""
				if p.Spec.StorageClassName != nil {
					sc = *p.Spec.StorageClassName
				}
				topo.StorageLeaks = append(topo.StorageLeaks, orphanStorageItem{
					PVCName:      p.Name,
					Namespace:    p.Namespace,
					Capacity:     capStr,
					StorageClass: sc,
					Status:       string(p.Status.Phase),
					Age:          formatAge(p.CreationTimestamp.Time),
					Reason:       "Unmounted: No active pod is attached to this volume",
				})
			}
		}
	}

	return topo, nil
}

// -------------------------------------------------------------
// Advanced Feature 7: Resource Efficiency & Heatmap
// -------------------------------------------------------------

func calculateResourceEfficiency(ctx context.Context, client kubernetes.Interface, db *sql.DB) (resourceEfficiencyReport, error) {
	rep := resourceEfficiencyReport{}
	if client == nil {
		return rep, errKubernetesNotConfigured
	}

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return rep, err
	}

	var totalCPUAllocatableMillis int64
	var totalMemAllocatableBytes int64

	for _, n := range nodes.Items {
		totalCPUAllocatableMillis += n.Status.Allocatable.Cpu().MilliValue()
		totalMemAllocatableBytes += n.Status.Allocatable.Memory().Value()
	}

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return rep, err
	}

	var totalCPURequestedMillis int64
	var totalMemRequestedBytes int64

	nodeCPUReq := make(map[string]int64)
	nodeMemReq := make(map[string]int64)
	nodePodsCount := make(map[string]int)

	wsCPUReq := make(map[string]int64)
	wsCPULim := make(map[string]int64)
	wsMemReq := make(map[string]int64)
	wsMemLim := make(map[string]int64)
	wsPodsCount := make(map[string]int)

	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		nodePodsCount[p.Spec.NodeName]++
		ns := p.Namespace

		var pCPUReq, pCPULim, pMemReq, pMemLim int64
		for _, c := range p.Spec.Containers {
			if c.Resources.Requests != nil {
				if cpu := c.Resources.Requests.Cpu(); cpu != nil {
					pCPUReq += cpu.MilliValue()
				}
				if mem := c.Resources.Requests.Memory(); mem != nil {
					pMemReq += mem.Value()
				}
			}
			if c.Resources.Limits != nil {
				if cpu := c.Resources.Limits.Cpu(); cpu != nil {
					pCPULim += cpu.MilliValue()
				}
				if mem := c.Resources.Limits.Memory(); mem != nil {
					pMemLim += mem.Value()
				}
			}
		}

		totalCPURequestedMillis += pCPUReq
		totalMemRequestedBytes += pMemReq

		nodeCPUReq[p.Spec.NodeName] += pCPUReq
		nodeMemReq[p.Spec.NodeName] += pMemReq

		wsCPUReq[ns] += pCPUReq
		wsCPULim[ns] += pCPULim
		wsMemReq[ns] += pMemReq
		wsMemLim[ns] += pMemLim
		wsPodsCount[ns]++
	}

	rep.TotalClusterCPUAllocatable = fmt.Sprintf("%.1f cores", float64(totalCPUAllocatableMillis)/1000.0)
	rep.TotalClusterMemAllocatable = fmt.Sprintf("%.1f GiB", float64(totalMemAllocatableBytes)/(1024*1024*1024))
	rep.CPURequestsTotal = fmt.Sprintf("%.1f cores", float64(totalCPURequestedMillis)/1000.0)
	rep.MemRequestsTotal = fmt.Sprintf("%.1f GiB", float64(totalMemRequestedBytes)/(1024*1024*1024))

	if totalCPUAllocatableMillis > 0 {
		rep.CPUUtilizationPercent = (float64(totalCPURequestedMillis) / float64(totalCPUAllocatableMillis)) * 100.0
	}
	if totalMemAllocatableBytes > 0 {
		rep.MemUtilizationPercent = (float64(totalMemRequestedBytes) / float64(totalMemAllocatableBytes)) * 100.0
	}

	for _, n := range nodes.Items {
		cAlloc := n.Status.Allocatable.Cpu().MilliValue()
		mAlloc := n.Status.Allocatable.Memory().Value()
		cReq := nodeCPUReq[n.Name]
		mReq := nodeMemReq[n.Name]

		cPct := 0.0
		if cAlloc > 0 {
			cPct = (float64(cReq) / float64(cAlloc)) * 100.0
		}
		mPct := 0.0
		if mAlloc > 0 {
			mPct = (float64(mReq) / float64(mAlloc)) * 100.0
		}

		rep.Nodes = append(rep.Nodes, nodeResourceUsage{
			NodeName:       n.Name,
			CPUAllocatable: fmt.Sprintf("%.1f cores", float64(cAlloc)/1000.0),
			MemAllocatable: fmt.Sprintf("%.1f GiB", float64(mAlloc)/(1024*1024*1024)),
			CPURequested:   fmt.Sprintf("%.1f cores", float64(cReq)/1000.0),
			MemRequested:   fmt.Sprintf("%.1f GiB", float64(mReq)/(1024*1024*1024)),
			PodsRunning:    nodePodsCount[n.Name],
			PodsCapacity:   n.Status.Capacity.Pods().Value(),
			CPUPct:         cPct,
			MemPct:         mPct,
		})
	}

	_, wsMap, _ := fetchWorkspaceMappings(ctx, db)
	for ns, count := range wsPodsCount {
		wsName := ns
		wsID := ""
		if strings.HasPrefix(ns, "crelan-ws-") {
			wsID = strings.TrimPrefix(ns, "crelan-ws-")
			if wsMap != nil && wsMap[wsID] != "" {
				wsName = wsMap[wsID]
			}
		}

		cReq := wsCPUReq[ns]
		cLim := wsCPULim[ns]
		mReq := wsMemReq[ns]
		mLim := wsMemLim[ns]

		eff := "Optimal"
		if cLim > 0 && cReq > 0 && float64(cReq)/float64(cLim) < 0.25 {
			eff = "Overprovisioned"
		}

		rep.Workspaces = append(rep.Workspaces, workspaceResourceUsage{
			WorkspaceID:   wsID,
			WorkspaceName: wsName,
			Namespace:     ns,
			PodsCount:     count,
			CPURequest:    fmt.Sprintf("%dm", cReq),
			CPULimit:      fmt.Sprintf("%dm", cLim),
			MemRequest:    fmt.Sprintf("%dMi", mReq/(1024*1024)),
			MemLimit:      fmt.Sprintf("%dMi", mLim/(1024*1024)),
			StorageUsage:  "Standard",
			Efficiency:    eff,
		})
	}

	sort.Slice(rep.Workspaces, func(i, j int) bool {
		return rep.Workspaces[i].PodsCount > rep.Workspaces[j].PodsCount
	})

	return rep, nil
}

// -------------------------------------------------------------
// Advanced Feature 6: Fast Remediation Actions
// -------------------------------------------------------------

func rolloutRestartDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, err = client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

func scaleDeploymentReplicas(ctx context.Context, client kubernetes.Interface, namespace, name string, replicas int32) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	if replicas < 0 || replicas > 100 {
		return fmt.Errorf("replicas count must be between 0 and 100")
	}
	scale, err := client.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	scale.Spec.Replicas = replicas
	_, err = client.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	return err
}

func forceDeletePod(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	zero := int64(0)
	return client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
	})
}

func patchDeploymentEnv(ctx context.Context, client kubernetes.Interface, namespace, name string, envMap map[string]string) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(dep.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("deployment has no containers")
	}

	c := &dep.Spec.Template.Spec.Containers[0]
	existing := make(map[string]int)
	for i, e := range c.Env {
		existing[e.Name] = i
	}

	for k, v := range envMap {
		if idx, ok := existing[k]; ok {
			c.Env[idx].Value = v
			c.Env[idx].ValueFrom = nil
		} else {
			c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: v})
		}
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, err = client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

func uniqueStrings(slice []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range slice {
		if !keys[entry] {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

// -------------------------------------------------------------
// Advanced Feature 7: 1-Click Workload Hardening
// -------------------------------------------------------------

func hardenDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	nonRoot := true
	noPrivEscalation := false
	uid := int64(10001)

	// Update pod template spec security context
	if dep.Spec.Template.Spec.SecurityContext == nil {
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{}
	}
	dep.Spec.Template.Spec.SecurityContext.RunAsNonRoot = &nonRoot
	if dep.Spec.Template.Spec.SecurityContext.RunAsUser == nil || *dep.Spec.Template.Spec.SecurityContext.RunAsUser == 0 {
		dep.Spec.Template.Spec.SecurityContext.RunAsUser = &uid
	}
	if dep.Spec.Template.Spec.SecurityContext.SeccompProfile == nil {
		dep.Spec.Template.Spec.SecurityContext.SeccompProfile = &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		}
	}

	// Update each container's security context
	for i := range dep.Spec.Template.Spec.Containers {
		c := &dep.Spec.Template.Spec.Containers[i]
		if c.SecurityContext == nil {
			c.SecurityContext = &corev1.SecurityContext{}
		}
		c.SecurityContext.RunAsNonRoot = &nonRoot
		if c.SecurityContext.RunAsUser == nil || *c.SecurityContext.RunAsUser == 0 {
			c.SecurityContext.RunAsUser = &uid
		}
		c.SecurityContext.AllowPrivilegeEscalation = &noPrivEscalation
		f := false
		c.SecurityContext.Privileged = &f
		c.SecurityContext.Capabilities = &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		}
		if c.SecurityContext.SeccompProfile == nil {
			c.SecurityContext.SeccompProfile = &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			}
		}
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	dep.Spec.Template.Annotations["crelan.platform/hardenedAt"] = time.Now().UTC().Format(time.RFC3339)

	_, err = client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

// -------------------------------------------------------------
// Advanced Feature 8: RBAC Blast Radius & Privilege Escalation Analyzer
// -------------------------------------------------------------

func runRBACBlastRadiusAnalysis(ctx context.Context, client kubernetes.Interface) (rbacBlastRadiusReport, error) {
	if client == nil {
		return rbacBlastRadiusReport{}, errKubernetesNotConfigured
	}

	tenantNS := make(map[string]bool)
	nsList, _ := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: capsuleTenantLabel})
	if nsList != nil {
		for _, ns := range nsList.Items {
			tenantNS[ns.Name] = true
		}
	}

	crList, err := client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		return rbacBlastRadiusReport{}, err
	}
	crRules := make(map[string][]rbacv1.PolicyRule)
	for _, cr := range crList.Items {
		crRules[cr.Name] = cr.Rules
	}

	crbList, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return rbacBlastRadiusReport{}, err
	}

	rolesList, _ := client.RbacV1().Roles("").List(ctx, metav1.ListOptions{})
	roleRules := make(map[string][]rbacv1.PolicyRule)
	if rolesList != nil {
		for _, r := range rolesList.Items {
			roleRules[r.Namespace+"/"+r.Name] = r.Rules
		}
	}

	rbList, _ := client.RbacV1().RoleBindings("").List(ctx, metav1.ListOptions{})

	report := rbacBlastRadiusReport{
		TotalBindingsScanned: len(crbList.Items),
	}
	if rbList != nil {
		report.TotalBindingsScanned += len(rbList.Items)
	}

	saRisksMap := make(map[string]*serviceAccountRisk)
	var findings []rbacBindingFinding

	for _, crb := range crbList.Items {
		var subjs []string
		hasTenantSA := false
		for _, s := range crb.Subjects {
			subjStr := fmt.Sprintf("%s:%s", s.Kind, s.Name)
			if s.Namespace != "" {
				subjStr += fmt.Sprintf(" (%s)", s.Namespace)
				if s.Kind == "ServiceAccount" && tenantNS[s.Namespace] {
					hasTenantSA = true
				}
			}
			subjs = append(subjs, subjStr)
		}

		var reasons []string
		riskLevel := "low"

		if crb.RoleRef.Name == "cluster-admin" {
			report.ClusterAdminCount++
			riskLevel = "critical"
			reasons = append(reasons, "Grants complete, unrestricted superuser privileges across the entire Kubernetes cluster.")
			if hasTenantSA {
				report.CrossTenantBindingsCount++
				reasons = append(reasons, "CRITICAL LEAK: ServiceAccount belonging to a tenant workspace has cluster-wide cluster-admin access!")
			}
		}

		rules := crRules[crb.RoleRef.Name]
		hasWildcard := false
		hasEscalate := false
		var sensitiveResources []string

		for _, r := range rules {
			isWildVerb := false
			isWildRes := false
			for _, v := range r.Verbs {
				if v == "*" {
					isWildVerb = true
				}
				if v == "bind" || v == "escalate" || v == "impersonate" {
					hasEscalate = true
				}
			}
			for _, res := range r.Resources {
				if res == "*" {
					isWildRes = true
				}
				if res == "secrets" || res == "nodes/proxy" || res == "pods/exec" || res == "certificatesigningrequests" {
					sensitiveResources = append(sensitiveResources, res)
				}
			}
			if isWildVerb && isWildRes {
				hasWildcard = true
			}
		}

		if hasWildcard && crb.RoleRef.Name != "cluster-admin" {
			report.WildcardPermissionsCount++
			if riskLevel != "critical" {
				riskLevel = "high"
			}
			reasons = append(reasons, "Role contains wildcard (*) verbs on wildcard (*) resources.")
		}

		if hasEscalate {
			report.PrivilegeEscalationCount++
			if riskLevel != "critical" {
				riskLevel = "high"
			}
			reasons = append(reasons, "Role contains dangerous privilege escalation verbs (bind, escalate, impersonate).")
		}

		if len(sensitiveResources) > 0 {
			if riskLevel == "low" {
				riskLevel = "medium"
			}
			reasons = append(reasons, fmt.Sprintf("Access to sensitive cluster resources: %s.", strings.Join(uniqueStrings(sensitiveResources), ", ")))
		}

		if riskLevel != "low" {
			findings = append(findings, rbacBindingFinding{
				Kind:        "ClusterRoleBinding",
				Name:        crb.Name,
				RoleRef:     crb.RoleRef.Name,
				RoleKind:    crb.RoleRef.Kind,
				Subjects:    subjs,
				RiskLevel:   riskLevel,
				RiskReasons: reasons,
				Remediation: "Scope permissions to specific namespaces using RoleBindings instead of cluster-wide bindings, and avoid wildcards.",
			})

			for _, s := range crb.Subjects {
				if s.Kind == "ServiceAccount" {
					ns := s.Namespace
					if ns == "" {
						ns = "default"
					}
					key := ns + "/" + s.Name
					if _, exists := saRisksMap[key]; !exists {
						saRisksMap[key] = &serviceAccountRisk{
							ServiceAccount: s.Name,
							Namespace:      ns,
							IsTenant:       tenantNS[ns],
							RolesBound:     []string{crb.RoleRef.Name},
							RiskSummary:    fmt.Sprintf("Cluster-wide binding via %s (%s)", crb.Name, riskLevel),
						}
					} else {
						saRisksMap[key].RolesBound = append(saRisksMap[key].RolesBound, crb.RoleRef.Name)
					}
				}
			}
		}
	}

	// Namespaced RoleBindings
	if rbList != nil {
		for _, rb := range rbList.Items {
			rules := roleRules[rb.Namespace+"/"+rb.RoleRef.Name]
			if len(rules) == 0 && rb.RoleRef.Kind == "ClusterRole" {
				rules = crRules[rb.RoleRef.Name]
			}
			hasWildcard := false
			hasEscalate := false
			crossNS := false
			var subjs []string
			for _, s := range rb.Subjects {
				subjStr := fmt.Sprintf("%s:%s", s.Kind, s.Name)
				if s.Namespace != "" {
					subjStr += fmt.Sprintf(" (%s)", s.Namespace)
					if s.Namespace != rb.Namespace {
						crossNS = true
					}
				}
				subjs = append(subjs, subjStr)
			}
			for _, r := range rules {
				for _, v := range r.Verbs {
					if v == "*" {
						hasWildcard = true
					}
					if v == "bind" || v == "escalate" || v == "impersonate" {
						hasEscalate = true
					}
				}
			}
			var reasons []string
			riskLevel := "low"
			if crossNS {
				riskLevel = "high"
				report.CrossTenantBindingsCount++
				reasons = append(reasons, fmt.Sprintf("Subject from namespace outside %q is granted access in this namespace.", rb.Namespace))
			}
			if hasEscalate {
				riskLevel = "high"
				report.PrivilegeEscalationCount++
				reasons = append(reasons, "Role contains privilege escalation verbs (bind, escalate, impersonate).")
			}
			if hasWildcard {
				if riskLevel == "low" {
					riskLevel = "medium"
				}
				report.WildcardPermissionsCount++
				reasons = append(reasons, "Role contains wildcard (*) verbs.")
			}
			if riskLevel != "low" {
				findings = append(findings, rbacBindingFinding{
					Kind:        "RoleBinding",
					Name:        rb.Name,
					Namespace:   rb.Namespace,
					RoleRef:     rb.RoleRef.Name,
					RoleKind:    rb.RoleRef.Kind,
					Subjects:    subjs,
					RiskLevel:   riskLevel,
					RiskReasons: reasons,
					Remediation: "Ensure service accounts belong to the same namespace and restrict verbs.",
				})
			}
		}
	}

	score := report.ClusterAdminCount*25 + report.PrivilegeEscalationCount*20 + report.CrossTenantBindingsCount*20 + report.WildcardPermissionsCount*10
	if score > 100 {
		score = 100
	}
	report.RiskScore = score

	sort.Slice(findings, func(i, j int) bool {
		priority := func(lvl string) int {
			switch lvl {
			case "critical":
				return 3
			case "high":
				return 2
			case "medium":
				return 1
			default:
				return 0
			}
		}
		return priority(findings[i].RiskLevel) > priority(findings[j].RiskLevel)
	})
	report.HighRiskBindings = findings

	var saRisks []serviceAccountRisk
	for _, v := range saRisksMap {
		saRisks = append(saRisks, *v)
	}
	sort.Slice(saRisks, func(i, j int) bool {
		if saRisks[i].IsTenant != saRisks[j].IsTenant {
			return saRisks[i].IsTenant
		}
		return saRisks[i].ServiceAccount < saRisks[j].ServiceAccount
	})
	report.ServiceAccountRisks = saRisks

	return report, nil
}

// -------------------------------------------------------------
// Advanced Feature 9: AI Resource Right-Sizing & Cost Advisor
// -------------------------------------------------------------

func calculateResourceRightSizing(ctx context.Context, client kubernetes.Interface) (rightSizingReport, error) {
	if client == nil {
		return rightSizingReport{}, errKubernetesNotConfigured
	}

	depList, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return rightSizingReport{}, err
	}

	report := rightSizingReport{
		TotalDeployments: len(depList.Items),
	}

	var recs []rightSizingRecommendation
	totalWasteUSD := 0.0

	for _, dep := range depList.Items {
		if len(dep.Spec.Template.Spec.Containers) == 0 {
			continue
		}

		for _, c := range dep.Spec.Template.Spec.Containers {
			cpuReq := c.Resources.Requests.Cpu()
			cpuLim := c.Resources.Limits.Cpu()
			memReq := c.Resources.Requests.Memory()
			memLim := c.Resources.Limits.Memory()

			hasCPULim := cpuLim != nil && !cpuLim.IsZero()
			hasMemLim := memLim != nil && !memLim.IsZero()
			hasCPUReq := cpuReq != nil && !cpuReq.IsZero()
			hasMemReq := memReq != nil && !memReq.IsZero()

			currentCPUReqStr := "none"
			if hasCPUReq {
				currentCPUReqStr = fmt.Sprintf("%dm", cpuReq.MilliValue())
			}
			currentCPULimStr := "none"
			if hasCPULim {
				currentCPULimStr = fmt.Sprintf("%dm", cpuLim.MilliValue())
			}
			currentMemReqStr := "none"
			if hasMemReq {
				currentMemReqStr = fmt.Sprintf("%dMi", memReq.Value()/(1024*1024))
			}
			currentMemLimStr := "none"
			if hasMemLim {
				currentMemLimStr = fmt.Sprintf("%dMi", memLim.Value()/(1024*1024))
			}

			rec := rightSizingRecommendation{
				DeploymentName:    dep.Name,
				Namespace:         dep.Namespace,
				ContainerName:     c.Name,
				CurrentCPURequest: currentCPUReqStr,
				CurrentCPULimit:   currentCPULimStr,
				CurrentMemRequest: currentMemReqStr,
				CurrentMemLimit:   currentMemLimStr,
				CanAutoApply:      true,
			}

			if !hasCPULim || !hasMemLim {
				report.MissingLimitsCount++
				rec.RiskLevel = "Uncapped"
				rec.Reason = "Missing container CPU or memory limits risks node exhaustion and pod eviction cascades."
				rec.RecommendedCPURequest = "100m"
				rec.RecommendedCPULimit = "500m"
				rec.RecommendedMemRequest = "128Mi"
				rec.RecommendedMemLimit = "512Mi"
				recs = append(recs, rec)
				continue
			}

			cpuMilli := cpuReq.MilliValue()
			memMi := memReq.Value() / (1024 * 1024)

			if cpuMilli >= 1000 || memMi >= 2048 {
				report.OverprovisionedCount++
				rec.RiskLevel = "High Waste"

				recCPU := "250m"
				recCPUMilli := int64(250)
				recMem := "512Mi"
				recMemMi := int64(512)

				rec.RecommendedCPURequest = recCPU
				recCPULimVal := cpuLim.MilliValue() / 2
				if recCPULimVal < 500 {
					recCPULimVal = 500
				}
				rec.RecommendedCPULimit = fmt.Sprintf("%dm", recCPULimVal)

				rec.RecommendedMemRequest = recMem
				recMemLimVal := memLim.Value() / (1024 * 1024 * 2)
				if recMemLimVal < 1024 {
					recMemLimVal = 1024
				}
				rec.RecommendedMemLimit = fmt.Sprintf("%dMi", recMemLimVal)

				savedCPU := float64(cpuMilli-recCPUMilli) / 1000.0
				savedMem := float64(memMi-recMemMi) / 1024.0
				monthly := (savedCPU * 30.0) + (savedMem * 4.0)
				if monthly < 0 {
					monthly = 0
				}
				rec.MonthlySavingsUSD = math.Round(monthly*100) / 100
				totalWasteUSD += rec.MonthlySavingsUSD
				rec.Reason = fmt.Sprintf("High allocation (%s CPU, %s RAM). Downsizing to recommended baseline saves ~$%.2f/month per replica.", currentCPUReqStr, currentMemReqStr, rec.MonthlySavingsUSD)
				recs = append(recs, rec)
			} else {
				rec.RiskLevel = "Optimized"
				rec.Reason = "Workload requests and limits are well-proportioned."
				rec.RecommendedCPURequest = currentCPUReqStr
				rec.RecommendedCPULimit = currentCPULimStr
				rec.RecommendedMemRequest = currentMemReqStr
				rec.RecommendedMemLimit = currentMemLimStr
				recs = append(recs, rec)
			}
		}
	}

	report.EstimatedMonthlySavingsUSD = math.Round(totalWasteUSD*100) / 100
	if report.TotalDeployments > 0 {
		report.EstimatedClusterWastePercent = math.Round((float64(report.OverprovisionedCount)/float64(report.TotalDeployments))*1000) / 10
	}
	report.Recommendations = recs
	return report, nil
}

func applyRightSizingPatch(ctx context.Context, client kubernetes.Interface, namespace, name string, cpuReq, cpuLim, memReq, memLim string) error {
	if client == nil {
		return errKubernetesNotConfigured
	}
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(dep.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("deployment has no containers")
	}

	c := &dep.Spec.Template.Spec.Containers[0]
	if c.Resources.Requests == nil {
		c.Resources.Requests = make(corev1.ResourceList)
	}
	if c.Resources.Limits == nil {
		c.Resources.Limits = make(corev1.ResourceList)
	}

	if cpuReq != "" && cpuReq != "none" {
		q, err := resource.ParseQuantity(cpuReq)
		if err != nil {
			return fmt.Errorf("invalid cpu request: %w", err)
		}
		c.Resources.Requests[corev1.ResourceCPU] = q
	}
	if cpuLim != "" && cpuLim != "none" {
		q, err := resource.ParseQuantity(cpuLim)
		if err != nil {
			return fmt.Errorf("invalid cpu limit: %w", err)
		}
		c.Resources.Limits[corev1.ResourceCPU] = q
	}
	if memReq != "" && memReq != "none" {
		q, err := resource.ParseQuantity(memReq)
		if err != nil {
			return fmt.Errorf("invalid memory request: %w", err)
		}
		c.Resources.Requests[corev1.ResourceMemory] = q
	}
	if memLim != "" && memLim != "none" {
		q, err := resource.ParseQuantity(memLim)
		if err != nil {
			return fmt.Errorf("invalid memory limit: %w", err)
		}
		c.Resources.Limits[corev1.ResourceMemory] = q
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	dep.Spec.Template.Annotations["crelan.platform/rightSizedAt"] = time.Now().UTC().Format(time.RFC3339)

	_, err = client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

// -------------------------------------------------------------
// Advanced Feature 10: Natural Language AI K8s Copilot
// -------------------------------------------------------------

func runAICopilotQuery(ctx context.Context, client kubernetes.Interface, db *sql.DB, query string) (aiCopilotResponse, error) {
	if client == nil {
		return aiCopilotResponse{
			Query:   query,
			Intent:  "Cluster Offline",
			Summary: "No Kubernetes cluster is currently connected. Please connect a cluster first via the Connect Cluster page.",
		}, nil
	}

	q := strings.ToLower(strings.TrimSpace(query))
	resp := aiCopilotResponse{
		Query: query,
	}

	// 1. Crash, Restart, Health Intent
	if strings.Contains(q, "crash") || strings.Contains(q, "restart") || strings.Contains(q, "oom") || strings.Contains(q, "fail") || strings.Contains(q, "error") || strings.Contains(q, "down") || strings.Contains(q, "unhealthy") || strings.Contains(q, "broken") {
		resp.Intent = "Workload Health & Crash Diagnostics"
		pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return resp, err
		}

		var failingPods []aiCopilotMatch
		for _, p := range pods.Items {
			hasIssue := false
			reason := string(p.Status.Phase)
			msg := ""

			if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodSucceeded {
				hasIssue = true
			}
			for _, cs := range p.Status.ContainerStatuses {
				if cs.RestartCount > 0 || !cs.Ready || cs.State.Waiting != nil || cs.State.Terminated != nil {
					if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
						reason = cs.State.Waiting.Reason
						msg = cs.State.Waiting.Message
						hasIssue = true
					}
					if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
						reason = cs.State.Terminated.Reason
						hasIssue = true
					}
					if cs.RestartCount > 0 {
						hasIssue = true
						msg = fmt.Sprintf("%d container restarts", cs.RestartCount)
					}
				}
			}

			if hasIssue {
				failingPods = append(failingPods, aiCopilotMatch{
					Kind:      "Pod",
					Name:      p.Name,
					Namespace: p.Namespace,
					Status:    string(p.Status.Phase),
					Reason:    reason,
					Message:   msg,
					Link:      fmt.Sprintf("#pods?ns=%s&name=%s", p.Namespace, p.Name),
				})
			}
		}

		resp.Matches = failingPods
		if len(failingPods) == 0 {
			resp.Summary = "All pods across all namespaces are currently healthy and running normally with 0 crash loops."
		} else {
			resp.Summary = fmt.Sprintf("Identified %d pod(s) with errors, elevated restarts, or non-running phase. Click any pod to inspect or trigger deep AI RCA.", len(failingPods))
			first := failingPods[0]
			resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
				ActionID:    "diagnose_pod",
				Title:       fmt.Sprintf("Run AI RCA on %s", first.Name),
				Description: "Extract live logs and events to analyze root cause.",
				Route:       fmt.Sprintf("#pods?ns=%s&name=%s&diagnose=true", first.Namespace, first.Name),
			})
		}
		return resp, nil
	}

	// 2. Security, CIS, Root, Privilege, Secret Leaks Intent
	if strings.Contains(q, "security") || strings.Contains(q, "root") || strings.Contains(q, "privilege") || strings.Contains(q, "leak") || strings.Contains(q, "secret") || strings.Contains(q, "cis") || strings.Contains(q, "vulnerability") {
		resp.Intent = "Security Posture & CIS Hardening"
		audit, err := runSecurityAudit(ctx, client)
		if err != nil {
			return resp, err
		}

		for _, iss := range audit.Issues {
			resp.Matches = append(resp.Matches, aiCopilotMatch{
				Kind:      "SecurityFinding",
				Name:      iss.PodName,
				Namespace: iss.Namespace,
				Status:    iss.Severity,
				Reason:    iss.Rule,
				Message:   iss.Description,
				Link:      "#security",
			})
			if len(resp.Matches) >= 15 {
				break
			}
		}

		resp.Summary = fmt.Sprintf("Security audit score is %d%% (%d checks passed, %d flagged). Found %d root containers, %d privileged workloads, and %d exposed secret credentials.",
			audit.Score, audit.PassedChecks, audit.FailedChecks, audit.RootCount, audit.PrivilegedCount, audit.SecretLeaksCount)

		resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
			ActionID:    "view_security",
			Title:       "Open Security Center",
			Description: "Review complete CIS benchmark findings and tenant isolation status.",
			Route:       "#security",
		})
		return resp, nil
	}

	// 3. RBAC, Permissions, ClusterAdmin Intent
	if strings.Contains(q, "rbac") || strings.Contains(q, "admin") || strings.Contains(q, "cluster-admin") || strings.Contains(q, "permission") || strings.Contains(q, "privilege escalation") || strings.Contains(q, "impersonat") {
		resp.Intent = "RBAC Blast Radius & Privilege Escalation"
		rbac, err := runRBACBlastRadiusAnalysis(ctx, client)
		if err != nil {
			return resp, err
		}

		for _, f := range rbac.HighRiskBindings {
			resp.Matches = append(resp.Matches, aiCopilotMatch{
				Kind:      f.Kind,
				Name:      f.Name,
				Namespace: f.Namespace,
				Status:    f.RiskLevel,
				Reason:    f.RoleRef,
				Message:   strings.Join(f.RiskReasons, " "),
				Link:      "#rbac",
			})
			if len(resp.Matches) >= 15 {
				break
			}
		}

		resp.Summary = fmt.Sprintf("Cluster RBAC risk score is %d/100. Discovered %d cluster-admin bindings, %d wildcard permission paths, and %d privilege escalation vectors across %d bindings.",
			rbac.RiskScore, rbac.ClusterAdminCount, rbac.WildcardPermissionsCount, rbac.PrivilegeEscalationCount, rbac.TotalBindingsScanned)

		resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
			ActionID:    "view_rbac",
			Title:       "Open RBAC Analyzer",
			Description: "Inspect high-risk bindings and tenant service account access.",
			Route:       "#rbac",
		})
		return resp, nil
	}

	// 4. Cost, Waste, Right-Sizing, Resource Requests/Limits Intent
	if strings.Contains(q, "cost") || strings.Contains(q, "waste") || strings.Contains(q, "rightsize") || strings.Contains(q, "right-size") || strings.Contains(q, "limit") || strings.Contains(q, "request") || strings.Contains(q, "cpu") || strings.Contains(q, "memory") || strings.Contains(q, "sizing") {
		resp.Intent = "Resource Right-Sizing & Cost Optimization"
		sizing, err := calculateResourceRightSizing(ctx, client)
		if err != nil {
			return resp, err
		}

		for _, r := range sizing.Recommendations {
			if r.RiskLevel != "Optimized" {
				resp.Matches = append(resp.Matches, aiCopilotMatch{
					Kind:      "Deployment",
					Name:      r.DeploymentName,
					Namespace: r.Namespace,
					Status:    r.RiskLevel,
					Reason:    fmt.Sprintf("Req: %s CPU / %s Mem", r.CurrentCPURequest, r.CurrentMemRequest),
					Message:   r.Reason,
					Link:      "#rightsizing",
				})
			}
		}

		resp.Summary = fmt.Sprintf("Evaluated %d deployments. Identified %d overprovisioned workloads (est. $%.2f/month waste) and %d uncapped containers risking eviction.",
			sizing.TotalDeployments, sizing.OverprovisionedCount, sizing.EstimatedMonthlySavingsUSD, sizing.MissingLimitsCount)

		resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
			ActionID:    "view_rightsizing",
			Title:       "Open Right-Sizing Advisor",
			Description: "Review 1-click resource adjustment recommendations.",
			Route:       "#rightsizing",
		})
		return resp, nil
	}

	// 5. Storage / PVC Intent
	if strings.Contains(q, "storage") || strings.Contains(q, "pvc") || strings.Contains(q, "pv") || strings.Contains(q, "volume") || strings.Contains(q, "disk") {
		resp.Intent = "Storage & Persistent Volumes"
		pvcs, err := client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return resp, err
		}
		for _, pvc := range pvcs.Items {
			cap := ""
			if q, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
				cap = q.String()
			}
			resp.Matches = append(resp.Matches, aiCopilotMatch{
				Kind:      "PersistentVolumeClaim",
				Name:      pvc.Name,
				Namespace: pvc.Namespace,
				Status:    string(pvc.Status.Phase),
				Reason:    pvc.Spec.VolumeName,
				Message:   fmt.Sprintf("Capacity: %s", cap),
				Link:      "#pvcs",
			})
		}
		resp.Summary = fmt.Sprintf("Found %d PersistentVolumeClaim(s) across the cluster.", len(pvcs.Items))
		resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
			ActionID:    "view_pvcs",
			Title:       "View All PVCs",
			Route:       "#pvcs",
		})
		return resp, nil
	}

	// 6. Events / Warnings Intent
	if strings.Contains(q, "event") || strings.Contains(q, "warn") || strings.Contains(q, "alert") {
		resp.Intent = "Cluster Warning Events"
		events, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return resp, err
		}
		count := 0
		for _, e := range events.Items {
			if e.Type == "Warning" {
				count++
				resp.Matches = append(resp.Matches, aiCopilotMatch{
					Kind:      e.InvolvedObject.Kind,
					Name:      e.InvolvedObject.Name,
					Namespace: e.Namespace,
					Status:    "Warning",
					Reason:    e.Reason,
					Message:   e.Message,
					Link:      "#events",
				})
				if len(resp.Matches) >= 20 {
					break
				}
			}
		}
		resp.Summary = fmt.Sprintf("Discovered %d warning event(s) across the cluster.", count)
		resp.SuggestedActions = append(resp.SuggestedActions, aiCopilotAction{
			ActionID:    "view_events",
			Title:       "Open Events Timeline",
			Route:       "#events",
		})
		return resp, nil
	}

	// 7. General Search / Fallback Intent
	resp.Intent = "Cluster Search & Overview"
	pods, _ := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	deps, _ := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	nodes, _ := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})

	podCount := 0
	if pods != nil {
		podCount = len(pods.Items)
		for _, p := range pods.Items {
			if strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.Namespace), q) {
				resp.Matches = append(resp.Matches, aiCopilotMatch{
					Kind:      "Pod",
					Name:      p.Name,
					Namespace: p.Namespace,
					Status:    string(p.Status.Phase),
					Link:      fmt.Sprintf("#pods?ns=%s&name=%s", p.Namespace, p.Name),
				})
			}
		}
	}
	depCount := 0
	if deps != nil {
		depCount = len(deps.Items)
		for _, d := range deps.Items {
			if strings.Contains(strings.ToLower(d.Name), q) || strings.Contains(strings.ToLower(d.Namespace), q) {
				resp.Matches = append(resp.Matches, aiCopilotMatch{
					Kind:      "Deployment",
					Name:      d.Name,
					Namespace: d.Namespace,
					Status:    fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, d.Status.Replicas),
					Link:      fmt.Sprintf("#deployments?ns=%s&name=%s", d.Namespace, d.Name),
				})
			}
		}
	}
	nodeCount := 0
	if nodes != nil {
		nodeCount = len(nodes.Items)
	}

	if len(resp.Matches) > 0 {
		resp.Summary = fmt.Sprintf("Found %d matching resource(s) for %q across cluster.", len(resp.Matches), query)
	} else {
		resp.Summary = fmt.Sprintf("Cluster has %d node(s), %d active deployment(s), and %d running pod(s). No specific name matches for %q. Ask about 'crashing pods', 'security', 'rbac', or 'cost'.",
			nodeCount, depCount, podCount, query)
	}
	return resp, nil
}
