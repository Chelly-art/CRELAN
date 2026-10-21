package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	managedServiceWorkspaceLabel = "crelan.io/workspace-id"
	managedServiceNameLabel      = "crelan.io/service-name"
	managedServiceTypeLabel      = "crelan.io/service-type"
	managedServiceOwnerLabel     = "app.kubernetes.io/managed-by"
)

var (
	managedServiceNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$`)
	valkeyClusterGVR          = k8sschema.GroupVersionResource{Group: "valkey.io", Version: "v1alpha1", Resource: "valkeyclusters"}
	rabbitmqClusterGVR        = k8sschema.GroupVersionResource{Group: "rabbitmq.com", Version: "v1beta1", Resource: "rabbitmqclusters"}
	managedServiceSecretGVR   = k8sschema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	managedServicePolicyGVR   = k8sschema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	managedServiceQuotaGVR    = k8sschema.GroupVersionResource{Version: "v1", Resource: "resourcequotas"}
	managedServiceCRDGVR      = k8sschema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	rabbitmqUserGVR           = k8sschema.GroupVersionResource{Group: "rabbitmq.com", Version: "v1beta1", Resource: "users"}
	rabbitmqPermissionGVR     = k8sschema.GroupVersionResource{Group: "rabbitmq.com", Version: "v1beta1", Resource: "permissions"}
)

type managedServiceService struct {
	auth        *authService
	provisioner *kubernetesProvisioner
	deployments *deploymentService
}

func (s *managedServiceService) currentProvisioner() *kubernetesProvisioner {
	if s.deployments != nil {
		if provisioner, ok := s.deployments.currentProvisioner().(*kubernetesProvisioner); ok {
			return provisioner
		}
		return nil
	}
	return s.provisioner
}

type managedServiceInput struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
	Storage string `json:"storage,omitempty"`
}

type managedServiceRecord struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Version         string `json:"version"`
	Status          string `json:"status"`
	Endpoint        string `json:"endpoint"`
	Port            int    `json:"port"`
	Storage         string `json:"storage"`
	Message         string `json:"message,omitempty"`
	ProductionReady bool   `json:"production_ready"`
	Backup          struct {
		Supported bool   `json:"supported"`
		Status    string `json:"status"`
	} `json:"backup"`
}

func (s *managedServiceService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/service-catalog", s.catalog)
	mux.HandleFunc("GET /api/v1/services", s.list)
	mux.HandleFunc("POST /api/v1/services", s.create)
	mux.HandleFunc("GET /api/v1/services/{id}", s.get)
	mux.HandleFunc("DELETE /api/v1/services/{id}", s.delete)
}

func (s *managedServiceService) identity(r *http.Request) (int64, string, error) {
	if s.auth == nil {
		return 0, "", errors.New("authentication is not configured")
	}
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil || workspaceID < 1 {
		return 0, "", errors.New("sign in required")
	}
	if s.currentProvisioner() == nil || s.currentProvisioner().dynamicClient == nil {
		return 0, "", errKubernetesNotConfigured
	}
	namespace, err := s.currentProvisioner().workspaceNamespace(r.Context(), workspaceID)
	return workspaceID, namespace, err
}

func (s *managedServiceService) catalog(w http.ResponseWriter, r *http.Request) {
	_, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	services := []map[string]any{
		{
			"type": "valkey", "name": "Valkey", "description": "Preview only: sharded Valkey managed by the Valkey Operator. Storage is per node (six nodes total). The operator is early development, uses a v1alpha1 API, requires Kubernetes 1.31+, and does not provide backups or a production HA guarantee.",
			"available": s.operatorAvailable(r.Context(), namespace, valkeyClusterGVR), "versions": []string{"operator-default"}, "default_version": "operator-default", "storage_options": []string{"5Gi", "10Gi"},
			"backups_supported": false, "preview": true, "production_ready": false,
		},
		{
			"type": "rabbitmq", "name": "RabbitMQ", "description": "Three-node quorum-queue RabbitMQ managed by the RabbitMQ Cluster Operator. Requires the Messaging Topology Operator to provision a separate app-scoped user; cluster administrator credentials are never returned. Storage is per node (three nodes total). Operator-managed backups are not configured, so production readiness depends on an external backup plan.",
			"available": s.operatorAvailable(r.Context(), namespace, rabbitmqClusterGVR), "versions": []string{"operator-default"}, "default_version": "operator-default", "storage_options": []string{"5Gi", "10Gi", "20Gi"},
			"backups_supported": false, "preview": false, "production_ready": false,
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

func (s *managedServiceService) operatorAvailable(ctx context.Context, namespace string, gvr k8sschema.GroupVersionResource) bool {
	_, err := s.currentProvisioner().dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || !s.operatorSchemaSupported(ctx, gvr) {
		return false
	}
	if gvr.Group == "rabbitmq.com" {
		for _, item := range []struct {
			gvr     k8sschema.GroupVersionResource
			crdName string
			fields  [][]string
		}{{rabbitmqUserGVR, "users.rabbitmq.com", [][]string{{"spec", "properties", "rabbitmqClusterReference"}, {"spec", "properties", "importCredentialsSecret"}}},
			{rabbitmqPermissionGVR, "permissions.rabbitmq.com", [][]string{{"spec", "properties", "rabbitmqClusterReference"}, {"spec", "properties", "user"}, {"spec", "properties", "vhost"}, {"spec", "properties", "permissions"}}}} {
			if _, err := s.currentProvisioner().dynamicClient.Resource(item.gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1}); err != nil || !s.crdSchemaHasFields(ctx, item.crdName, item.gvr.Version, item.fields...) {
				return false
			}
		}
	}
	return true
}

func (s *managedServiceService) operatorSchemaSupported(ctx context.Context, gvr k8sschema.GroupVersionResource) bool {
	crdName := gvr.Resource + "." + gvr.Group
	fields := [][]string{{"spec", "properties", "persistence"}}
	if gvr.Group == "rabbitmq.com" {
		fields = append(fields, []string{"spec", "properties", "affinity"}, []string{"spec", "properties", "rabbitmq", "properties", "additionalConfig"})
	} else {
		fields = append(fields, []string{"spec", "properties", "users"}, []string{"spec", "properties", "scheduling"})
	}
	return s.crdSchemaHasFields(ctx, crdName, gvr.Version, fields...)
}

func (s *managedServiceService) crdSchemaHasFields(ctx context.Context, crdName, servedVersion string, fields ...[]string) bool {
	crd, err := s.currentProvisioner().dynamicClient.Resource(managedServiceCRDGVR).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	versions, found, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	if err != nil || !found {
		return false
	}
	for _, raw := range versions {
		version, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(version, "name")
		served, _, _ := unstructured.NestedBool(version, "served")
		if name != servedVersion || !served {
			continue
		}
		for _, fieldPath := range fields {
			path := append([]string{"schema", "openAPIV3Schema", "properties"}, fieldPath...)
			_, exists, _ := unstructured.NestedFieldNoCopy(version, path...)
			if !exists {
				return false
			}
		}
		return true
	}
	return false
}

func (s *managedServiceService) list(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	items := make([]managedServiceRecord, 0)
	for _, entry := range managedServiceDefinitions {
		list, listErr := s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: managedServiceWorkspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
		if listErr != nil {
			if isManagedServiceCRDMissing(listErr) {
				continue
			}
			s.writeFailure(w, listErr)
			return
		}
		for i := range list.Items {
			items = append(items, managedServiceRecordFromResource(&list.Items[i], entry, namespace))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *managedServiceService) get(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	resource, entry, err := s.findOwned(r.Context(), namespace, workspaceID, r.PathValue("id"))
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedServiceRecordFromResource(resource, entry, namespace))
}

func (s *managedServiceService) create(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	var input managedServiceInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Provide a valid service name and type")
		return
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if !managedServiceNamePattern.MatchString(input.Name) || len(input.Name) > 40 {
		writeError(w, http.StatusBadRequest, "Service name must be a lowercase DNS name of at most 40 characters")
		return
	}
	entry, ok := managedServiceDefinition(input.Type)
	if !ok {
		writeError(w, http.StatusBadRequest, "Supported service types are valkey and rabbitmq")
		return
	}
	if input.Version == "" {
		input.Version = "operator-default"
	}
	if input.Version != "operator-default" {
		writeError(w, http.StatusBadRequest, "This service currently uses the version selected by its installed operator")
		return
	}
	if input.Storage == "" {
		input.Storage = "10Gi"
	}
	if input.Storage != "5Gi" && input.Storage != "10Gi" && input.Storage != "20Gi" {
		writeError(w, http.StatusBadRequest, "Storage must be one of 5Gi, 10Gi, or 20Gi")
		return
	}
	if input.Type == "valkey" && input.Storage == "20Gi" {
		writeError(w, http.StatusBadRequest, "Valkey storage options are 5Gi or 10Gi per node")
		return
	}
	if !s.operatorAvailable(r.Context(), namespace, entry.gvr) {
		writeError(w, http.StatusServiceUnavailable, entry.name+" operator CRD is missing, inaccessible, or does not support required managed credentials")
		return
	}
	if err := s.ensureNameAvailable(r.Context(), namespace, input.Name); err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.withinWorkspaceLimits(w, r, workspaceID, namespace, entry, input.Storage) {
		return
	}
	if err := s.ensureQuota(r.Context(), workspaceID, namespace); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not apply workspace managed-service resource limits")
		return
	}
	password, err := managedServicePassword()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not generate service credentials")
		return
	}
	labels := map[string]any{
		managedServiceWorkspaceLabel: strconv.FormatInt(workspaceID, 10),
		managedServiceNameLabel:      input.Name,
		managedServiceTypeLabel:      input.Type,
		managedServiceOwnerLabel:     "crelan",
	}
	secretName := "svc-" + input.Name + "-auth"
	secret := serviceCredentialsSecret(namespace, secretName, input, password, labels)
	createdSecret, err := s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Create(r.Context(), secret, metav1.CreateOptions{})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	cr := managedServiceCustomResource(entry, namespace, input, secretName, labels)
	created, err := s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Create(r.Context(), cr, metav1.CreateOptions{})
	if err != nil {
		_ = s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		s.writeFailure(w, err)
		return
	}
	if entry.typeName == "rabbitmq" {
		if err := s.createRabbitMQAppIdentity(r.Context(), namespace, workspaceID, input.Name, managedServiceUsername(entry, input.Name), secretName, created); err != nil {
			_ = s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
			_ = s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
			s.writeFailure(w, err)
			return
		}
	}
	if err := s.createNetworkPolicy(r.Context(), namespace, workspaceID, entry, input.Name, created); err != nil {
		if entry.typeName == "rabbitmq" {
			s.deleteRabbitMQAppIdentity(context.Background(), namespace, input.Name)
		}
		_ = s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
		_ = s.currentProvisioner().dynamicClient.Resource(managedServicePolicyGVR).Namespace(namespace).Delete(context.Background(), "svc-"+input.Name+"-ingress", metav1.DeleteOptions{})
		_ = s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		writeError(w, http.StatusServiceUnavailable, "Could not apply service network isolation")
		return
	}
	setDynamicOwnerReference(createdSecret, created)
	if _, err := s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Update(r.Context(), createdSecret, metav1.UpdateOptions{}); err != nil {
		if entry.typeName == "rabbitmq" {
			s.deleteRabbitMQAppIdentity(context.Background(), namespace, input.Name)
		}
		_ = s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
		_ = s.currentProvisioner().dynamicClient.Resource(managedServicePolicyGVR).Namespace(namespace).Delete(context.Background(), "svc-"+input.Name+"-ingress", metav1.DeleteOptions{})
		_ = s.currentProvisioner().dynamicClient.Resource(managedServiceSecretGVR).Namespace(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		s.writeFailure(w, err)
		return
	}
	response := map[string]any{
		"service":     managedServiceRecordFromResource(created, entry, namespace),
		"credentials": map[string]any{"username": managedServiceUsername(entry, input.Name), "password": password, "endpoint": serviceEndpoint(entry, input.Name, namespace), "port": entry.port},
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, action, details, created_at) VALUES (?, 'service.create', ?, ?)`, workspaceID, input.Name+" ("+input.Type+")", time.Now().UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusCreated, response)
}

func (s *managedServiceService) delete(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	if r.URL.Query().Get("confirm_data_loss") != "true" {
		writeError(w, http.StatusConflict, "This service uses persistent storage. Repeat with ?confirm_data_loss=true to confirm deletion and possible data loss")
		return
	}
	resource, entry, err := s.findOwned(r.Context(), namespace, workspaceID, r.PathValue("id"))
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if entry.typeName == "rabbitmq" {
		if err := s.deleteRabbitMQAppIdentity(r.Context(), namespace, resource.GetName()); err != nil {
			s.writeFailure(w, err)
			return
		}
	}
	uid := resource.GetUID()
	err = s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Delete(r.Context(), resource.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, action, details, created_at) VALUES (?, 'service.delete', ?, ?)`, workspaceID, resource.GetName()+" ("+entry.typeName+")", time.Now().UTC().Format(time.RFC3339Nano))
	w.WriteHeader(http.StatusAccepted)
}

func (s *managedServiceService) findOwned(ctx context.Context, namespace string, workspaceID int64, id string) (*unstructured.Unstructured, managedServiceDescriptor, error) {
	if !managedServiceNamePattern.MatchString(id) {
		return nil, managedServiceDescriptor{}, apierrors.NewNotFound(k8sschema.GroupResource{Resource: "services"}, id)
	}
	for _, entry := range managedServiceDefinitions {
		resource, err := s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Get(ctx, id, metav1.GetOptions{})
		if isManagedServiceCRDMissing(err) || apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, managedServiceDescriptor{}, err
		}
		labels := resource.GetLabels()
		if labels[managedServiceWorkspaceLabel] == strconv.FormatInt(workspaceID, 10) && labels[managedServiceNameLabel] == id && labels[managedServiceTypeLabel] == entry.typeName {
			return resource, entry, nil
		}
	}
	return nil, managedServiceDescriptor{}, apierrors.NewNotFound(k8sschema.GroupResource{Resource: "services"}, id)
}

func (s *managedServiceService) ensureNameAvailable(ctx context.Context, namespace, name string) error {
	for _, entry := range managedServiceDefinitions {
		_, err := s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return apierrors.NewAlreadyExists(k8sschema.GroupResource{Group: entry.gvr.Group, Resource: entry.gvr.Resource}, name)
		}
		if apierrors.IsNotFound(err) || isManagedServiceCRDMissing(err) {
			continue
		}
		return err
	}
	return nil
}

type managedServiceDescriptor struct {
	typeName string
	name     string
	gvr      k8sschema.GroupVersionResource
	username string
	port     int
	backups  bool
}

var managedServiceDefinitions = []managedServiceDescriptor{
	{typeName: "valkey", name: "Valkey", gvr: valkeyClusterGVR, username: "default", port: 6379, backups: false},
	{typeName: "rabbitmq", name: "RabbitMQ", gvr: rabbitmqClusterGVR, port: 5672, backups: false},
}

func managedServiceUsername(entry managedServiceDescriptor, name string) string {
	if entry.typeName == "rabbitmq" {
		return "app-" + name
	}
	return entry.username
}

func managedServiceDefinition(serviceType string) (managedServiceDescriptor, bool) {
	for _, definition := range managedServiceDefinitions {
		if definition.typeName == serviceType {
			return definition, true
		}
	}
	return managedServiceDescriptor{}, false
}

func serviceCredentialsSecret(namespace, name string, input managedServiceInput, password string, labels map[string]any) *unstructured.Unstructured {
	username := "default"
	port := "6379"
	secretData := map[string]any{"password": base64.StdEncoding.EncodeToString([]byte(password))}
	metadataLabels := map[string]any{}
	for key, value := range labels {
		metadataLabels[key] = value
	}
	if input.Type == "rabbitmq" {
		username = "app-" + input.Name
		port = "5672"
		host := serviceEndpoint(managedServiceDefinitions[1], input.Name, namespace)
		metadataLabels["rabbitmq.com/topology-operator"] = "true"
		secretData = map[string]any{
			"host":     base64.StdEncoding.EncodeToString([]byte(host)),
			"username": base64.StdEncoding.EncodeToString([]byte(username)),
			"password": base64.StdEncoding.EncodeToString([]byte(password)),
			"port":     base64.StdEncoding.EncodeToString([]byte(port)),
			"provider": base64.StdEncoding.EncodeToString([]byte("rabbitmq")),
			"type":     base64.StdEncoding.EncodeToString([]byte("rabbitmq")),
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": metadataLabels},
		"type":     "Opaque", "data": secretData,
	}}
}

func managedServiceCustomResource(entry managedServiceDescriptor, namespace string, input managedServiceInput, secret string, labels map[string]any) *unstructured.Unstructured {
	metadata := map[string]any{"name": input.Name, "namespace": namespace, "labels": labels}
	var apiVersion, kind string
	var spec map[string]any
	if entry.typeName == "valkey" {
		apiVersion, kind = "valkey.io/v1alpha1", "ValkeyCluster"
		spec = map[string]any{
			"shards": int64(3), "replicas": int64(1), "workloadType": "StatefulSet",
			"persistence": map[string]any{"size": input.Storage, "reclaimPolicy": "Retain"},
			"resources":   map[string]any{"requests": map[string]any{"cpu": "250m", "memory": "512Mi"}, "limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
			"scheduling": map[string]any{
				"node": map[string]any{"spread": map[string]any{"shard": map[string]any{"mode": "Preferred"}}},
				"zone": map[string]any{"spread": map[string]any{"shard": map[string]any{"mode": "Preferred"}}},
			},
			"users": []any{map[string]any{
				"name": "default", "passwordSecret": map[string]any{"name": secret, "keys": []any{"password"}},
				"commands": map[string]any{"allow": []any{"@all"}},
				"keys":     map[string]any{"readWrite": []any{"*"}}, "channels": map[string]any{"patterns": []any{"*"}},
			}},
			"exporter": map[string]any{"enabled": false},
		}
	} else {
		apiVersion, kind = "rabbitmq.com/v1beta1", "RabbitmqCluster"
		spec = map[string]any{
			"replicas":    int64(3),
			"persistence": map[string]any{"storage": input.Storage},
			"resources":   map[string]any{"requests": map[string]any{"cpu": "500m", "memory": "1Gi"}, "limits": map[string]any{"cpu": "1", "memory": "2Gi"}},
			"rabbitmq":    map[string]any{"additionalConfig": "default_queue_type = quorum"},
			"affinity": map[string]any{"podAntiAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"labelSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/component": "rabbitmq", "app.kubernetes.io/name": input.Name}},
					"topologyKey":   "kubernetes.io/hostname",
				}},
			}},
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": metadata, "spec": spec}}
}

func serviceEndpoint(entry managedServiceDescriptor, name, namespace string) string {
	if entry.typeName == "valkey" {
		return "valkey-" + name + "." + namespace + ".svc.cluster.local"
	}
	return name + "." + namespace + ".svc.cluster.local"
}

func (s *managedServiceService) createRabbitMQAppIdentity(ctx context.Context, namespace string, workspaceID int64, name, username, credentialsSecret string, cluster *unstructured.Unstructured) error {
	labels := map[string]any{
		managedServiceWorkspaceLabel: strconv.FormatInt(workspaceID, 10),
		managedServiceNameLabel:      name,
		managedServiceTypeLabel:      "rabbitmq",
		managedServiceOwnerLabel:     "crelan",
	}
	user := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rabbitmq.com/v1beta1", "kind": "User",
		"metadata": map[string]any{"name": username, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"rabbitmqClusterReference": map[string]any{"name": name},
			"importCredentialsSecret":  map[string]any{"name": credentialsSecret},
		},
	}}
	setDynamicOwnerReference(user, cluster)
	createdUser, err := s.currentProvisioner().dynamicClient.Resource(rabbitmqUserGVR).Namespace(namespace).Create(ctx, user, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	permissionName := name + "-app-permission"
	permission := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rabbitmq.com/v1beta1", "kind": "Permission",
		"metadata": map[string]any{"name": permissionName, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"rabbitmqClusterReference": map[string]any{"name": name},
			"user":                     username,
			"vhost":                    "/",
			"permissions": map[string]any{
				"configure": ".*",
				"write":     ".*",
				"read":      ".*",
			},
		},
	}}
	setDynamicOwnerReference(permission, cluster)
	if _, err := s.currentProvisioner().dynamicClient.Resource(rabbitmqPermissionGVR).Namespace(namespace).Create(ctx, permission, metav1.CreateOptions{}); err != nil {
		_ = s.currentProvisioner().dynamicClient.Resource(rabbitmqUserGVR).Namespace(namespace).Delete(context.Background(), createdUser.GetName(), metav1.DeleteOptions{})
		return err
	}
	return nil
}

func (s *managedServiceService) deleteRabbitMQAppIdentity(ctx context.Context, namespace, name string) error {
	permissionName := name + "-app-permission"
	username := "app-" + name
	for _, resource := range []struct {
		gvr  k8sschema.GroupVersionResource
		name string
	}{{rabbitmqPermissionGVR, permissionName}, {rabbitmqUserGVR, username}} {
		err := s.currentProvisioner().dynamicClient.Resource(resource.gvr).Namespace(namespace).Delete(ctx, resource.name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) && !isManagedServiceCRDMissing(err) {
			return err
		}
	}
	return nil
}

func managedServiceRecordFromResource(resource *unstructured.Unstructured, entry managedServiceDescriptor, namespace string) managedServiceRecord {
	status := "provisioning"
	message := ""
	if entry.typeName == "valkey" {
		if current, _, _ := unstructured.NestedString(resource.Object, "status", "state"); current != "" {
			status = strings.ToLower(current)
		}
		message, _, _ = unstructured.NestedString(resource.Object, "status", "reason")
		if detail, _, _ := unstructured.NestedString(resource.Object, "status", "message"); detail != "" {
			message = detail
		}
	} else {
		conditions, _, _ := unstructured.NestedSlice(resource.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typeName, _, _ := unstructured.NestedString(condition, "type")
			conditionStatus, _, _ := unstructured.NestedString(condition, "status")
			if typeName == "AllReplicasReady" && conditionStatus == "True" {
				status = "ready"
			}
			if typeName == "ReconcileSuccess" && conditionStatus == "False" {
				status = "degraded"
				message, _, _ = unstructured.NestedString(condition, "message")
			}
		}
	}
	version := "operator-default"
	if entry.typeName == "rabbitmq" {
		if image, _, _ := unstructured.NestedString(resource.Object, "spec", "image"); image != "" {
			version = image
		}
	} else if image, _, _ := unstructured.NestedString(resource.Object, "spec", "image"); image != "" {
		version = image
	}
	storagePath := []string{"spec", "persistence", "size"}
	if entry.typeName == "rabbitmq" {
		storagePath = []string{"spec", "persistence", "storage"}
	}
	storage, _, _ := unstructured.NestedString(resource.Object, storagePath...)
	backupStatus := "not_supported_by_operator"
	if entry.backups {
		backupStatus = "not_configured"
	}
	productionReady := false
	if entry.typeName == "valkey" {
		message = "Preview operator service; production availability is not guaranteed. " + message
	}
	return managedServiceRecord{
		ID: resource.GetName(), Name: resource.GetName(), Type: entry.typeName, Version: version, Status: status,
		Endpoint: serviceEndpoint(entry, resource.GetName(), namespace), Port: entry.port, Storage: storage, Message: strings.TrimSpace(message), ProductionReady: productionReady,
		Backup: struct {
			Supported bool   `json:"supported"`
			Status    string `json:"status"`
		}{Supported: entry.backups, Status: backupStatus},
	}
}

func (s *managedServiceService) createNetworkPolicy(ctx context.Context, namespace string, workspaceID int64, entry managedServiceDescriptor, name string, owner *unstructured.Unstructured) error {
	matchLabels := map[string]any{}
	ports := []any{}
	if entry.typeName == "valkey" {
		matchLabels["valkey.io/cluster"] = name
		ports = append(ports, map[string]any{"protocol": "TCP", "port": int64(6379)}, map[string]any{"protocol": "TCP", "port": int64(16379)})
	} else {
		matchLabels["app.kubernetes.io/component"] = "rabbitmq"
		matchLabels["app.kubernetes.io/name"] = name
		ports = append(ports, map[string]any{"protocol": "TCP", "port": int64(4369)}, map[string]any{"protocol": "TCP", "port": int64(5672)}, map[string]any{"protocol": "TCP", "port": int64(15672)}, map[string]any{"protocol": "TCP", "port": int64(25672)})
	}
	policyName := "svc-" + name + "-ingress"
	labels := map[string]any{managedServiceWorkspaceLabel: strconv.FormatInt(workspaceID, 10), managedServiceNameLabel: name, managedServiceTypeLabel: entry.typeName, managedServiceOwnerLabel: "crelan"}
	policy := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": policyName, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": matchLabels},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": namespace}}}},
				"ports": ports,
			}},
		},
	}}
	if _, err := s.currentProvisioner().dynamicClient.Resource(managedServicePolicyGVR).Namespace(namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		return err
	}
	created, err := s.currentProvisioner().dynamicClient.Resource(managedServicePolicyGVR).Namespace(namespace).Get(ctx, policyName, metav1.GetOptions{})
	if err == nil {
		setDynamicOwnerReference(created, owner)
		_, err = s.currentProvisioner().dynamicClient.Resource(managedServicePolicyGVR).Namespace(namespace).Update(ctx, created, metav1.UpdateOptions{})
	}
	return err
}

func (s *managedServiceService) withinWorkspaceLimits(w http.ResponseWriter, r *http.Request, workspaceID int64, namespace string, requested managedServiceDescriptor, requestedStorage string) bool {
	const maxServices = 3
	const maxStorageGi = 100
	requestedNodes := 3
	if requested.typeName == "valkey" {
		requestedNodes = 6
	}
	total, allocatedGi := 0, serviceStorageGi(requestedStorage)*requestedNodes
	for _, entry := range managedServiceDefinitions {
		list, err := s.currentProvisioner().dynamicClient.Resource(entry.gvr).Namespace(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: managedServiceWorkspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
		if err != nil {
			if isManagedServiceCRDMissing(err) {
				continue
			}
			s.writeFailure(w, err)
			return false
		}
		total += len(list.Items)
		for i := range list.Items {
			storagePath := []string{"spec", "persistence", "size"}
			if entry.typeName == "rabbitmq" {
				storagePath = []string{"spec", "persistence", "storage"}
			}
			stored, _, _ := unstructured.NestedString(list.Items[i].Object, storagePath...)
			nodes := 3
			if entry.typeName == "valkey" {
				nodes = 6
			}
			allocatedGi += serviceStorageGi(stored) * nodes
		}
	}
	if total >= maxServices {
		writeError(w, http.StatusTooManyRequests, "Workspace managed-service limit reached (3 services)")
		return false
	}
	if allocatedGi > maxStorageGi {
		writeError(w, http.StatusTooManyRequests, "Workspace managed-service storage limit exceeded (100Gi total across all service replicas)")
		return false
	}
	return true
}

func serviceStorageGi(value string) int {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "Gi") {
		n, err := strconv.Atoi(strings.TrimSuffix(value, "Gi"))
		if err == nil && n > 0 {
			if n > 1000 {
				return 1001
			}
			return n
		}
	}
	return 1001
}

func (s *managedServiceService) ensureQuota(ctx context.Context, workspaceID int64, namespace string) error {
	name := "crelan-managed-service-limits"
	labels := map[string]any{managedServiceWorkspaceLabel: strconv.FormatInt(workspaceID, 10), managedServiceOwnerLabel: "crelan"}
	hard := map[string]any{
		"requests.cpu": "8", "requests.memory": "16Gi", "limits.cpu": "16", "limits.memory": "32Gi",
		"requests.storage": "2000Gi", "count/pods": "50", "count/persistentvolumeclaims": "50",
		"count/valkeyclusters.valkey.io": "3", "count/rabbitmqclusters.rabbitmq.com": "3",
	}
	quota := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ResourceQuota",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec":     map[string]any{"hard": hard},
	}}
	resource := s.currentProvisioner().dynamicClient.Resource(managedServiceQuotaGVR).Namespace(namespace)
	current, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = resource.Create(ctx, quota, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.GetLabels()[managedServiceWorkspaceLabel] != strconv.FormatInt(workspaceID, 10) || current.GetLabels()[managedServiceOwnerLabel] != "crelan" {
		return errors.New("managed service quota is owned by another identity")
	}
	quota.SetResourceVersion(current.GetResourceVersion())
	_, err = resource.Update(ctx, quota, metav1.UpdateOptions{})
	return err
}

func managedServicePassword() (string, error) {
	raw := make([]byte, 36)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func setDynamicOwnerReference(child, owner *unstructured.Unstructured) {
	gv, err := k8sschema.ParseGroupVersion(owner.GetAPIVersion())
	if err != nil || owner.GetUID() == "" {
		return
	}
	controller, block := true, true
	refs := []any{map[string]any{
		"apiVersion": owner.GetAPIVersion(), "kind": owner.GetKind(), "name": owner.GetName(), "uid": string(owner.GetUID()),
		"controller": controller, "blockOwnerDeletion": block,
	}}
	_ = gv
	_ = unstructured.SetNestedSlice(child.Object, refs, "metadata", "ownerReferences")
}

func isManagedServiceCRDMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "the server could not find the requested resource") || strings.Contains(message, "no matches for kind")
}

func (s *managedServiceService) authorizeWrite(w http.ResponseWriter, r *http.Request, workspaceID int64) bool {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return false
	}
	userID, selectedWorkspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil || selectedWorkspaceID != workspaceID {
		writeError(w, http.StatusForbidden, "Workspace access required")
		return false
	}
	var role string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&role); err != nil {
		writeError(w, http.StatusForbidden, "Workspace access required")
		return false
	}
	if role != "owner" && role != "admin" && role != "developer" {
		writeError(w, http.StatusForbidden, "Workspace write access required")
		return false
	}
	return true
}

func (s *managedServiceService) writeFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errKubernetesNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "Kubernetes service provisioning is not configured")
	case strings.Contains(err.Error(), "sign in required"):
		writeError(w, http.StatusUnauthorized, "Sign in required")
	case strings.Contains(err.Error(), "has not been provisioned"), strings.Contains(err.Error(), "not assigned to its expected Capsule tenant"):
		writeError(w, http.StatusConflict, err.Error())
	case apierrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "Service not found")
	case apierrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "A service with that name already exists")
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusServiceUnavailable, "The platform Kubernetes identity cannot manage services in this tenant")
	case isManagedServiceCRDMissing(err):
		writeError(w, http.StatusServiceUnavailable, "The required service operator CRD is not installed")
	default:
		writeError(w, http.StatusInternalServerError, "Service operation failed")
	}
}
