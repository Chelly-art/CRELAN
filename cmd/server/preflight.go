package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// preflightRequiredRBAC is returned by the admin readiness API so operators can
// compare the connected identity with the permissions this control plane uses.
var preflightRequiredRBAC = map[string][]string{
	"core":                 {"nodes get,list", "namespaces get", "resourcequotas get,list,create,update,patch,delete", "persistentvolumes get,list", "persistentvolumeclaims get,list,create,update,patch,delete", "secrets get,list,create,update,patch,delete", "services get,list,create,update,patch,delete", "configmaps get,list,create,update,patch,delete", "pods get,list"},
	"apps":                 {"deployments get,list,create,update,patch,delete"},
	"storage.k8s.io":       {"storageclasses get,list"},
	"apiextensions.k8s.io": {"customresourcedefinitions get"},
	"capsule.clastix.io":   {"tenants get,create"},
	"postgresql.cnpg.io":   {"clusters get,list,create,update,patch,delete", "backups get,list,create,update,patch,delete", "scheduledbackups get,list,create,update,patch,delete"},
	"mysql.oracle.com":     {"innodbclusters get,list,create,update,patch,delete", "mysqlbackups get,list,create,update,patch,delete"},
	"valkey.io":            {"valkeyclusters get,list,create,update,patch,delete"},
	"rabbitmq.com":         {"rabbitmqclusters get,list,create,update,patch,delete", "users get,list,create,update,patch,delete", "permissions get,list,create,update,patch,delete"},
	"networking.k8s.io":    {"networkpolicies get,list,create,update,patch,delete", "ingresses get,list,create,update,patch,delete"},
	"kpack.io":             {"builds get,list,create,update,patch,delete"},
}

type preflightResource struct {
	name        string
	group       string
	version     string
	resource    string
	kind        string
	remediation string
}

var preflightResources = []preflightResource{
	{name: "Capsule Tenant API", group: "capsule.clastix.io", version: "v1beta2", resource: capsuleTenantResource, kind: "Tenant", remediation: "Install the Capsule operator with the v1beta2 Tenant CRD"},
	{name: "CloudNativePG API", group: "postgresql.cnpg.io", version: "v1", resource: "clusters", kind: "Cluster", remediation: "Install the CloudNativePG operator and its v1 Cluster CRD"},
	{name: "CloudNativePG backup API", group: "postgresql.cnpg.io", version: "v1", resource: "backups", kind: "Backup", remediation: "Install the CloudNativePG operator with the v1 Backup CRD before enabling backups"},
	{name: "CloudNativePG scheduled backup API", group: "postgresql.cnpg.io", version: "v1", resource: "scheduledbackups", kind: "ScheduledBackup", remediation: "Install the CloudNativePG operator with the v1 ScheduledBackup CRD before enabling backups"},
	{name: "MySQL Operator API", group: "mysql.oracle.com", version: "v2", resource: "innodbclusters", kind: "InnoDBCluster", remediation: "Install the Oracle MySQL operator and its v2 InnoDBCluster CRD"},
	{name: "MySQL backup API", group: "mysql.oracle.com", version: "v2", resource: "mysqlbackups", kind: "MySQLBackup", remediation: "Install the Oracle MySQL operator with the v2 MySQLBackup CRD before enabling backups"},
	{name: "Valkey Operator API", group: "valkey.io", version: "v1alpha1", resource: "valkeyclusters", kind: "ValkeyCluster", remediation: "Install the Valkey operator and its v1alpha1 ValkeyCluster CRD"},
	{name: "RabbitMQ Operator API", group: "rabbitmq.com", version: "v1beta1", resource: "rabbitmqclusters", kind: "RabbitmqCluster", remediation: "Install the RabbitMQ Cluster Operator and its v1beta1 RabbitmqCluster CRD"},
	{name: "RabbitMQ Topology User API", group: "rabbitmq.com", version: "v1beta1", resource: "users", kind: "User", remediation: "Install the RabbitMQ Messaging Topology Operator and its v1beta1 User CRD"},
	{name: "RabbitMQ Topology Permission API", group: "rabbitmq.com", version: "v1beta1", resource: "permissions", kind: "Permission", remediation: "Install the RabbitMQ Messaging Topology Operator and its v1beta1 Permission CRD"},
}

func (s *adminService) clusterPreflightChecks(ctx context.Context, requestedWorkspace string) []readinessCheck {
	checks := make([]readinessCheck, 0, 12)
	add := func(name string, ready bool, detail string) {
		checks = append(checks, readinessCheck{Name: name, Ready: ready, Detail: detail})
	}
	if err := s.auth.db.PingContext(ctx); err != nil {
		add("Control plane database", false, "SQLite connection failed; check DATABASE_PATH and filesystem access")
	} else {
		add("Control plane database", true, "Connected")
	}

	p := s.currentKubernetesProvisioner()
	if p == nil || p.client == nil {
		add("Kubernetes API", false, "No cluster credentials are configured; connect a cluster in the platform admin page")
		add("Workspace namespace and Capsule tenant", false, "Connect a cluster before checking workspace mapping")
		add("Allowed RWO storage classes", false, "Connect a cluster before checking storage classes")
		add("Allowed RWX storage classes", false, "Connect a cluster before checking storage classes")
		for _, resource := range preflightResources {
			add(resource.name, false, "Connect a cluster before checking the operator API")
		}
		return checks
	}
	if _, err := p.client.Discovery().ServerVersion(); err != nil {
		add("Kubernetes API", false, "Cluster API is unreachable or discovery is denied; verify cluster credentials and API access")
	} else {
		add("Kubernetes API", true, "Cluster API is reachable")
	}

	for _, resource := range preflightResources {
		ok, detail := preflightAPIResourceStatus(ctx, p, resource)
		add(resource.name, ok, detail)
	}
	if p.config.capsuleOwner == "" {
		add("Capsule tenant owner configuration", false, "Set CRELAN_CAPSULE_TENANT_OWNER to the Kubernetes user or group Capsule should assign as tenant owner")
	} else {
		add("Capsule tenant owner configuration", true, "Configured")
	}
	checks = append(checks, workspaceCapsuleMappingChecks(ctx, s, p, requestedWorkspace)...)
	checks = append(checks, preflightStorageChecks(ctx, p)...)

	nodes, err := p.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		add("Ready nodes", false, "Cannot list nodes; grant core/nodes get and list")
		add("Failure zones", false, "Cannot inspect node zone labels; grant core/nodes list")
	} else {
		ready := countReadyNodes(nodes.Items)
		zones := map[string]struct{}{}
		for _, node := range nodes.Items {
			if zone := node.Labels[corev1.LabelTopologyZone]; zone != "" {
				zones[zone] = struct{}{}
			}
		}
		add("Ready nodes", ready >= 3, fmt.Sprintf("%d ready; three are needed to place a three-instance HA database. Add ready worker nodes if fewer than three.", ready))
		add("Failure zones", len(zones) >= 3, fmt.Sprintf("%d distinct topology zones; three are recommended for zone-level HA. Label nodes with %q or add zones.", len(zones), corev1.LabelTopologyZone))
	}
	return checks
}

func preflightAPIResourceStatus(ctx context.Context, p *kubernetesProvisioner, expected preflightResource) (bool, string) {
	resources, err := p.client.Discovery().ServerResourcesForGroupVersion(expected.group + "/" + expected.version)
	if err != nil || resources == nil {
		return false, expected.remediation + "; also verify API discovery RBAC"
	}
	found := false
	for _, resource := range resources.APIResources {
		if resource.Name == expected.resource && resource.Kind == expected.kind {
			found = true
			break
		}
	}
	if !found {
		return false, expected.remediation
	}
	if p.dynamicClient == nil {
		return false, "API is discoverable, but its OpenAPI schema cannot be verified; reconnect the cluster"
	}
	crdName := expected.resource + "." + expected.group
	crd, err := p.dynamicClient.Resource(k8sschema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		return false, "API is discoverable, but CRD schema cannot be read; grant apiextensions.k8s.io/customresourcedefinitions get"
	}
	versions, _, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	if err != nil {
		return false, "CRD is present, but its served version schema is malformed"
	}
	for _, raw := range versions {
		version, ok := raw.(map[string]any)
		if !ok || version["name"] != expected.version || version["served"] != true {
			continue
		}
		if _, ok := version["schema"].(map[string]any); !ok {
			return false, "CRD version is served without an OpenAPI schema; reinstall or repair the operator CRD"
		}
		return true, "Required API resource and served OpenAPI schema are available; operator controller health still needs verification"
	}
	return false, "CRD exists, but the required API version is not served; upgrade the operator CRD"
}

func workspaceCapsuleMappingChecks(ctx context.Context, s *adminService, p *kubernetesProvisioner, requested string) []readinessCheck {
	workspaceID, err := selectPreflightWorkspace(ctx, s, requested)
	if err != nil {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: err.Error()}}
	}
	namespace := p.config.namespacePrefix + strconv.FormatInt(workspaceID, 10)
	tenant := p.config.tenantPrefix + strconv.FormatInt(workspaceID, 10)
	ns, err := p.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: "Workspace " + strconv.FormatInt(workspaceID, 10) + " namespace is missing or unreadable; verify namespace provisioning and grant core/namespaces get"}}
	}
	if ns.Labels[capsuleTenantLabel] != tenant || ns.Labels[workspaceLabel] != strconv.FormatInt(workspaceID, 10) {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: "Workspace namespace labels do not map to the expected Capsule tenant; repair the namespace labels before provisioning"}}
	}
	if p.dynamicClient == nil {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: "Kubernetes dynamic client is unavailable; reconnect the cluster"}}
	}
	resource := p.dynamicClient.Resource(k8sschema.GroupVersionResource{Group: "capsule.clastix.io", Version: "v1beta2", Resource: capsuleTenantResource})
	item, err := resource.Get(ctx, tenant, metav1.GetOptions{})
	if err != nil {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: "Expected Capsule tenant is missing or unreadable; create tenant " + tenant + " and grant Capsule Tenant get"}}
	}
	if item.GetLabels()[workspaceLabel] != strconv.FormatInt(workspaceID, 10) {
		return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: false, Detail: "Capsule tenant does not carry the expected workspace ownership label; repair tenant ownership metadata"}}
	}
	return []readinessCheck{{Name: "Workspace namespace and Capsule tenant", Ready: true, Detail: "Workspace " + strconv.FormatInt(workspaceID, 10) + " maps to its expected namespace and Capsule tenant"}}
}

func selectPreflightWorkspace(ctx context.Context, s *adminService, requested string) (int64, error) {
	var id int64
	if requested != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(requested), 10, 64)
		if err != nil || parsed < 1 {
			return 0, fmt.Errorf("workspace_id must be a positive workspace identifier")
		}
		if err := s.auth.db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE id = ?`, parsed).Scan(&id); err != nil {
			return 0, fmt.Errorf("workspace %d does not exist in the control plane", parsed)
		}
		return id, nil
	}
	if err := s.auth.db.QueryRowContext(ctx, `SELECT MIN(id) FROM workspaces`).Scan(&id); err != nil || id < 1 {
		return 0, fmt.Errorf("no workspace is available to check; create a workspace or pass ?workspace_id=<id>")
	}
	return id, nil
}

func preflightStorageChecks(ctx context.Context, p *kubernetesProvisioner) []readinessCheck {
	allowed := allowedAppStorageClasses()
	sharedAllowed := allowedSharedAppStorageClasses()
	classes, err := p.client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return []readinessCheck{
			{Name: "Allowed RWO storage classes", Ready: false, Detail: "Cannot list StorageClasses; grant storage.k8s.io/storageclasses get and list"},
			{Name: "Allowed RWX storage classes", Ready: false, Detail: "Cannot list StorageClasses; grant storage.k8s.io/storageclasses get and list"},
		}
	}
	byName := make(map[string]storagev1.StorageClass, len(classes.Items))
	for _, class := range classes.Items {
		byName[class.Name] = class
	}
	rwoCheck := preflightStorageClassSet("Allowed RWO storage classes", allowed, byName,
		"CRELAN_ALLOWED_APP_STORAGE_CLASSES", false)
	rwxCheck := preflightStorageClassSet("Allowed RWX storage classes", sharedAllowed, byName,
		"CRELAN_ALLOWED_APP_SHARED_STORAGE_CLASSES", true)
	if !hasDefaultStorageClass(classes.Items) {
		rwoCheck.Detail += "; no default class is set (set a default if operator-managed PVCs rely on it)"
	}
	return []readinessCheck{rwoCheck, rwxCheck}
}

func preflightStorageClassSet(checkName string, allowlist map[string]struct{}, classes map[string]storagev1.StorageClass, envName string, optional bool) readinessCheck {
	if len(allowlist) == 0 {
		if optional {
			return readinessCheck{Name: checkName, Ready: true, Detail: "Shared app storage is disabled; set " + envName + " to enable approved RWX classes"}
		}
		return readinessCheck{Name: checkName, Ready: false, Detail: "Set " + envName + " to one or more provisioned durable StorageClass names"}
	}
	valid := make([]string, 0, len(allowlist))
	missing := make([]string, 0)
	for name := range allowlist {
		class, ok := classes[name]
		if !ok || strings.TrimSpace(class.Provisioner) == "" {
			missing = append(missing, name)
			continue
		}
		valid = append(valid, name)
	}
	if len(valid) == 0 {
		return readinessCheck{Name: checkName, Ready: false, Detail: fmt.Sprintf("None of the configured classes %v exist with a provisioner; install/configure a CSI provisioner and verify %s", sortedStringsFromSet(allowlist), envName)}
	}
	detail := fmt.Sprintf("Provisioned classes available: %v", sortedStrings(valid))
	if len(missing) > 0 {
		detail += fmt.Sprintf("; configured but unavailable: %v", missing)
	}
	if optional {
		detail += "; confirm each listed CSI driver supports ReadWriteMany before enabling it"
	}
	return readinessCheck{Name: checkName, Ready: true, Detail: detail}
}

func hasDefaultStorageClass(classes []storagev1.StorageClass) bool {
	for _, class := range classes {
		if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" || class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			return true
		}
	}
	return false
}

func sortedStringsFromSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return sortedStrings(result)
}

func sortedStrings(result []string) []string {
	// Lists are small and sorting them makes API output stable without importing
	// or depending on any service-specific policy.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j] < result[i] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}
