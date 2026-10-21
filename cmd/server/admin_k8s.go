package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	PodName       string         `json:"pod_name"`
	Namespace     string         `json:"namespace"`
	Phase         string         `json:"phase"`
	Severity      string         `json:"severity"`
	Summary       string         `json:"summary"`
	ProbableCause string         `json:"probable_cause"`
	Confidence    string         `json:"confidence"`
	Evidence      []string       `json:"evidence"`
	Remediation   []aiAction     `json:"remediation"`
	RelevantLogs  string         `json:"relevant_logs,omitempty"`
	WarningEvents []eventSummary `json:"warning_events,omitempty"`
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
	Issues           []securityIssue      `json:"issues"`
	TenantIsolation  tenantIsolationAudit `json:"tenant_isolation"`
}

type securityIssue struct {
	Severity    string `json:"severity"`
	Rule        string `json:"rule"`
	PodName     string `json:"pod_name"`
	Namespace   string `json:"namespace"`
	Container   string `json:"container,omitempty"`
	Description string `json:"description"`
	Remediation string `json:"remediation"`
}

type tenantIsolationAudit struct {
	TotalTenantSpaces    int      `json:"total_tenant_namespaces"`
	EnforcedSpaces       int      `json:"enforced_namespaces"`
	MissingNetworkPolicy []string `json:"missing_network_policy"`
	Status               string   `json:"status"`
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

	recentLogs, _ := fetchPodLogs(ctx, client, namespace, podName, "", 50, false)
	report.RelevantLogs = recentLogs

	var evidences []string
	var actions []aiAction

	hasCrash := false
	hasOOM := false
	hasImageErr := false
	hasConfigErr := false

	for _, cs := range pod.Status.ContainerStatuses {
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

	if hasOOM {
		report.Severity = "critical"
		report.Summary = "Pod terminated due to Out Of Memory (OOMKilled - Exit Code 137)"
		report.ProbableCause = "The container process exceeded the allocated memory request/limit, causing the Linux kernel OOM-killer to terminate the container."
		actions = append(actions, aiAction{
			ActionID:    "increase_memory",
			Title:       "Increase Container Memory Limit",
			Description: "Raise memory limits in the deployment specification (e.g. increase from current limit to 512Mi or 1Gi).",
			AutoFixable: false,
		})
		actions = append(actions, aiAction{
			ActionID:    "restart_pod",
			Title:       "Restart Pod",
			Description: "Evict the pod to restart it immediately while you investigate application memory leak.",
			Command:     fmt.Sprintf("kubectl delete pod %s -n %s", podName, namespace),
			AutoFixable: true,
		})
	} else if hasImageErr {
		report.Severity = "critical"
		report.Summary = "Container image could not be pulled from registry (ImagePullBackOff / ErrImagePull)"
		report.ProbableCause = "The container image does not exist, the image tag is invalid, or the cluster lacks image pull secrets for a private registry."
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
		actions = append(actions, aiAction{
			ActionID:    "check_missing_secrets",
			Title:       "Inspect ConfigMaps and Secrets",
			Description: "Verify that all referenced environment secrets exist in namespace " + namespace + ".",
			AutoFixable: false,
		})
	} else if hasCrash {
		report.Severity = "critical"
		report.Summary = "Application runtime failure causing CrashLoopBackOff"
		report.ProbableCause = "The application crashed on startup or during health check. Common reasons include missing database connection string, invalid port binding, or uncaught exception."
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
	}

	report.Evidence = evidences
	report.Remediation = actions
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
// Advanced Feature 5: Security & Isolation Auditor
// -------------------------------------------------------------

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

	for _, p := range podList.Items {
		if p.Status.Phase == corev1.PodSucceeded {
			continue
		}

		for _, c := range p.Spec.Containers {
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
				issues = append(issues, securityIssue{
					Severity:    "high",
					Rule:        "ContainerRunsAsRoot",
					PodName:     p.Name,
					Namespace:   p.Namespace,
					Container:   c.Name,
					Description: fmt.Sprintf("Container %q is running with root permissions (UID 0).", c.Name),
					Remediation: "Set `securityContext.runAsNonRoot: true` or specify `runAsUser: 1000`.",
				})
			} else {
				passed++
			}

			if c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
				failed++
				issues = append(issues, securityIssue{
					Severity:    "critical",
					Rule:        "PrivilegedContainer",
					PodName:     p.Name,
					Namespace:   p.Namespace,
					Container:   c.Name,
					Description: fmt.Sprintf("Container %q is configured with `privileged: true`, bypassing Linux isolation.", c.Name),
					Remediation: "Remove `privileged: true` and request minimal Linux capabilities.",
				})
			} else {
				passed++
			}

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

	return securityAuditReport{
		TotalPodsScanned: len(podList.Items),
		PassedChecks:     passed,
		FailedChecks:     failed,
		Score:            score,
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
