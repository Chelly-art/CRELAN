package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

const externalConnectionLabel = "crelan.io/external-connection"

type connectionService struct {
	auth        *authService
	deployments *deploymentService
}

type externalConnectionInput struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Endpoint        string            `json:"endpoint,omitempty"`
	Host            string            `json:"host,omitempty"`
	Port            int               `json:"port,omitempty"`
	Database        string            `json:"database,omitempty"`
	ConnectionURL   string            `json:"connection_url,omitempty"`
	Username        string            `json:"username,omitempty"`
	Password        string            `json:"password,omitempty"`
	Bucket          string            `json:"bucket,omitempty"`
	Region          string            `json:"region,omitempty"`
	AccessKeyID     string            `json:"access_key_id,omitempty"`
	SecretAccessKey string            `json:"secret_access_key,omitempty"`
	EnvMapping      map[string]string `json:"env_mapping,omitempty"`
}

type externalConnectionMetadata struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Endpoint        string            `json:"endpoint,omitempty"`
	Host            string            `json:"host,omitempty"`
	Port            int               `json:"port,omitempty"`
	Database        string            `json:"database,omitempty"`
	Bucket          string            `json:"bucket,omitempty"`
	Region          string            `json:"region,omitempty"`
	SecretName      string            `json:"secret_name"`
	SourceServiceID string            `json:"source_service_id,omitempty"`
	SecretKeys      []string          `json:"secret_keys"`
	EnvMapping      map[string]string `json:"env_mapping,omitempty"`
	ClusterContext  string            `json:"cluster_context,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	UpdatedAt       string            `json:"updated_at,omitempty"`
}

func (s *connectionService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workspace/connections", s.list)
	mux.HandleFunc("POST /api/v1/workspace/connections", s.create)
	mux.HandleFunc("POST /api/v1/workspace/connections/from-managed-service", s.fromManagedService)
	mux.HandleFunc("GET /api/v1/workspace/connections/{id}", s.get)
	mux.HandleFunc("PUT /api/v1/workspace/connections/{id}", s.update)
	mux.HandleFunc("DELETE /api/v1/workspace/connections/{id}", s.delete)
	mux.HandleFunc("POST /api/v1/deployments/{id}/connections", s.attachToDeployment)
}

func (s *connectionService) fromManagedService(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, _, cluster, err := s.identity(r, true)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	var input struct {
		Name      string `json:"name"`
		Type      string `json:"type,omitempty"`
		ServiceID string `json:"service_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Provide a connection name and service_id")
		return
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	if input.Name == "" || !deploymentNamePattern.MatchString(input.Name) || len(input.Name) > 49 {
		writeError(w, http.StatusBadRequest, "Connection name must be a lowercase DNS name of at most 49 characters")
		return
	}
	engine, serviceName, err := parseManagedServiceID(input.ServiceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if engine == "" && input.Type != "" {
		engine = input.Type
	}
	if input.Type != "" && input.Type != engine {
		writeError(w, http.StatusBadRequest, "Connection type must match the managed service engine")
		return
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), workspaceID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if engine != "" && engine != "postgresql" && engine != "mysql" && engine != "valkey" && engine != "rabbitmq" {
		writeError(w, http.StatusBadRequest, "Supported managed service sources are postgresql, mysql, valkey, and rabbitmq")
		return
	}
	var service *unstructured.Unstructured
	var sourceSecret *corev1.Secret
	var gvk k8sschema.GroupVersionKind
	if engine == "valkey" || engine == "rabbitmq" {
		service, sourceSecret, gvk, err = s.findOwnedManagedOperatorService(r.Context(), cluster, workspaceID, namespace, engine, serviceName)
	} else {
		service, sourceSecret, gvk, err = s.findManagedDatabase(r.Context(), cluster, workspaceID, namespace, engine, serviceName)
		if err == nil && engine == "" {
			if service.GetAPIVersion() == "postgresql.cnpg.io/v1" {
				engine = "postgresql"
			} else {
				engine = "mysql"
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "ambiguous") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeFailure(w, err)
		return
	}
	data := map[string][]byte{}
	var usernameKey, passwordKey string
	port := 5432
	host := serviceName + "-rw." + namespace + ".svc.cluster.local"
	database := "app"
	urlScheme := engine
	if engine == "postgresql" {
		usernameKey, passwordKey = "username", "password"
	} else if engine == "mysql" {
		usernameKey, passwordKey = "appUser", "appPassword"
		port = 3306
		host = serviceName + "." + namespace + ".svc.cluster.local"
	} else if engine == "valkey" {
		usernameKey, passwordKey = "username", "password"
		port = 6379
		host = "valkey-" + serviceName + "." + namespace + ".svc.cluster.local"
		database = ""
		urlScheme = "redis"
	} else {
		usernameKey, passwordKey = "username", "password"
		port = 5672
		host = serviceName + "." + namespace + ".svc.cluster.local"
		database = ""
		urlScheme = "amqp"
	}
	username := string(sourceSecret.Data[usernameKey])
	password := string(sourceSecret.Data[passwordKey])
	if engine == "valkey" && username == "" {
		username = "default"
	}
	if username == "" || password == "" {
		writeError(w, http.StatusConflict, "Managed service credentials are not ready")
		return
	}
	data["HOST"] = []byte(host)
	data["PORT"] = []byte(strconv.Itoa(port))
	if database != "" {
		data["DATABASE"] = []byte(database)
	}
	data["USERNAME"] = []byte(username)
	data["PASSWORD"] = []byte(password)
	connectionURL := &url.URL{Scheme: urlScheme, User: url.UserPassword(username, password), Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if database != "" {
		connectionURL.Path = "/" + database
	} else if engine == "rabbitmq" {
		connectionURL.Path = "/"
	}
	data["CONNECTION_URL"] = []byte(connectionURL.String())
	labels := map[string]string{externalConnectionLabel: "true", workspaceLabel: strconv.FormatInt(workspaceID, 10), "crelan.io/connection-name": input.Name}
	annotations := map[string]string{
		"crelan.io/connection-type": engine, "crelan.io/connection-endpoint": host,
		"crelan.io/connection-host": host, "crelan.io/connection-port": strconv.Itoa(port),
		"crelan.io/connection-database": database, "crelan.io/cluster-context": s.activeContext(),
		"crelan.io/source-service-id": engine + ":" + serviceName,
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: externalConnectionSecretName(input.Name), Namespace: namespace, Labels: labels, Annotations: annotations,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind, Name: serviceName, UID: service.GetUID(), Controller: ptr(true), BlockOwnerDeletion: ptr(true)}},
	}, Type: corev1.SecretTypeOpaque, Data: data}
	created, err := cluster.client.CoreV1().Secrets(namespace).Create(r.Context(), secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		writeError(w, http.StatusConflict, "A connection with that name already exists")
		return
	}
	if err != nil {
		log.Printf("create managed service connection: %v", err)
		s.writeFailure(w, err)
		return
	}
	if err := s.deployments.recordAudit(r.Context(), workspaceID, userID, "connection.from_managed_service", engine+":"+serviceName+" -> "+input.Name); err != nil {
		log.Printf("audit managed service connection: %v", err)
	}
	writeJSON(w, http.StatusCreated, metadataFromConnectionSecret(created))
}

func (s *connectionService) activeContext() string {
	if s.deployments == nil {
		return ""
	}
	s.deployments.providerMu.RLock()
	defer s.deployments.providerMu.RUnlock()
	return s.deployments.activeContext
}

func parseManagedServiceID(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	engine, name, ok := strings.Cut(raw, ":")
	if !ok {
		engine, name, ok = strings.Cut(raw, "/")
	}
	if !ok {
		name = raw
		engine = ""
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || !databaseNamePattern.MatchString(name) {
		return "", "", errors.New("service_id must identify a managed service")
	}
	if engine != "" && engine != "postgresql" && engine != "mysql" && engine != "valkey" && engine != "rabbitmq" {
		return "", "", errors.New("supported managed service sources are postgresql, mysql, valkey, and rabbitmq")
	}
	return engine, name, nil
}

func (s *connectionService) findOwnedManagedOperatorService(ctx context.Context, cluster *kubernetesProvisioner, workspaceID int64, namespace, serviceType, name string) (*unstructured.Unstructured, *corev1.Secret, k8sschema.GroupVersionKind, error) {
	definition, ok := managedServiceDefinition(serviceType)
	if !ok {
		return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
	}
	service, err := cluster.dynamicClient.Resource(definition.gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
		}
		return nil, nil, k8sschema.GroupVersionKind{}, err
	}
	labels := service.GetLabels()
	if labels[managedServiceWorkspaceLabel] != strconv.FormatInt(workspaceID, 10) || labels[managedServiceNameLabel] != name || labels[managedServiceTypeLabel] != serviceType || labels[managedServiceOwnerLabel] != "crelan" {
		return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
	}
	secret, err := cluster.client.CoreV1().Secrets(namespace).Get(ctx, "svc-"+name+"-auth", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
		}
		return nil, nil, k8sschema.GroupVersionKind{}, err
	}
	secretLabels := secret.Labels
	if secretLabels[managedServiceWorkspaceLabel] != strconv.FormatInt(workspaceID, 10) || secretLabels[managedServiceNameLabel] != name || secretLabels[managedServiceTypeLabel] != serviceType || secretLabels[managedServiceOwnerLabel] != "crelan" {
		return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
	}
	g, err := k8sschema.ParseGroupVersion(service.GetAPIVersion())
	if err != nil || service.GetKind() == "" {
		return nil, nil, k8sschema.GroupVersionKind{}, errors.New("managed service has invalid Kubernetes identity")
	}
	return service, secret, g.WithKind(service.GetKind()), nil
}

func (s *connectionService) findManagedDatabase(ctx context.Context, cluster *kubernetesProvisioner, workspaceID int64, namespace, engine, name string) (*unstructured.Unstructured, *corev1.Secret, k8sschema.GroupVersionKind, error) {
	engines := []string{engine}
	if engine == "" {
		engines = []string{"postgresql", "mysql"}
	}
	var found *unstructured.Unstructured
	var foundEngine string
	for _, candidate := range engines {
		resource, err := cluster.dynamicClient.Resource(databaseGVR(candidate)).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, nil, k8sschema.GroupVersionKind{}, err
		}
		if resource.GetLabels()[databaseWorkspaceLabel] != strconv.FormatInt(workspaceID, 10) || resource.GetLabels()[databaseNameLabel] != name {
			continue
		}
		if found != nil {
			return nil, nil, k8sschema.GroupVersionKind{}, errors.New("service_id is ambiguous; prefix it with postgresql: or mysql:")
		}
		found, foundEngine = resource, candidate
	}
	if found == nil {
		return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
	}
	secret, err := cluster.client.CoreV1().Secrets(namespace).Get(ctx, "db-"+name+"-credentials", metav1.GetOptions{})
	if err != nil {
		return nil, nil, k8sschema.GroupVersionKind{}, err
	}
	if secret.Labels[databaseWorkspaceLabel] != strconv.FormatInt(workspaceID, 10) || secret.Labels[databaseNameLabel] != name {
		return nil, nil, k8sschema.GroupVersionKind{}, sql.ErrNoRows
	}
	gvk := k8sschema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"}
	if foundEngine == "mysql" {
		gvk = k8sschema.GroupVersionKind{Group: "mysql.oracle.com", Version: "v2", Kind: "InnoDBCluster"}
	}
	return found, secret, gvk, nil
}

func (s *connectionService) identity(r *http.Request, write bool) (int64, int64, string, *kubernetesProvisioner, error) {
	userID, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil {
		return 0, 0, "", nil, fmt.Errorf("session authentication required: %w", err)
	}
	var role string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, userID, workspaceID).Scan(&role); err != nil {
		return 0, 0, "", nil, err
	}
	if write && role != "owner" && role != "developer" {
		return 0, 0, "", nil, errors.New("workspace role does not allow connection changes")
	}
	provider, err := s.deployments.provisionerForContext("")
	if err != nil {
		return 0, 0, "", nil, err
	}
	cluster, ok := provider.(*kubernetesProvisioner)
	if !ok || cluster == nil {
		return 0, 0, "", nil, errKubernetesNotConfigured
	}
	return userID, workspaceID, role, cluster, nil
}

func (s *connectionService) list(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, _, cluster, err := s.identity(r, false)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), workspaceID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secrets, err := cluster.client.CoreV1().Secrets(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: externalConnectionLabel + "=true," + workspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
	if err != nil {
		log.Printf("list workspace external connections: %v", err)
		s.writeFailure(w, err)
		return
	}
	items := make([]externalConnectionMetadata, 0, len(secrets.Items))
	for i := range secrets.Items {
		if !ownsWorkspace(secrets.Items[i].Labels, workspaceID) {
			continue
		}
		items = append(items, metadataFromConnectionSecret(&secrets.Items[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": items})
}

func (s *connectionService) get(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, _, cluster, err := s.identity(r, false)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secret, err := s.getOwnedSecret(r.Context(), cluster, workspaceID, r.PathValue("id"))
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadataFromConnectionSecret(secret))
}

func (s *connectionService) create(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, _, cluster, err := s.identity(r, true)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	var input externalConnectionInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter valid connection details")
		return
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	if input.Name == "" || !deploymentNamePattern.MatchString(input.Name) || len(input.Name) > 49 {
		writeError(w, http.StatusBadRequest, "Connection name must be a lowercase DNS name of at most 49 characters")
		return
	}
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if err := validateExternalConnection(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), workspaceID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secretName := externalConnectionSecretName(input.Name)
	secrets := cluster.client.CoreV1().Secrets(namespace)
	if _, err := secrets.Get(r.Context(), secretName, metav1.GetOptions{}); err == nil {
		writeError(w, http.StatusConflict, "A connection with that name already exists")
		return
	} else if !apierrors.IsNotFound(err) {
		writeError(w, http.StatusBadGateway, "Could not check existing connection")
		return
	}
	secret, err := s.connectionSecret(r.Context(), cluster, workspaceID, userID, namespace, input, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := secrets.Create(r.Context(), secret, metav1.CreateOptions{})
	if err != nil {
		log.Printf("create external connection secret: %v", err)
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, metadataFromConnectionSecret(created))
}

func (s *connectionService) update(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	userID, workspaceID, _, cluster, err := s.identity(r, true)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	current, err := s.getOwnedSecret(r.Context(), cluster, workspaceID, r.PathValue("id"))
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	var input externalConnectionInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter valid connection details")
		return
	}
	input.Name = current.Labels["crelan.io/connection-name"]
	if input.Name == "" {
		writeError(w, http.StatusConflict, "Connection metadata is invalid")
		return
	}
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if input.Type == "" {
		input.Type = current.Annotations["crelan.io/connection-type"]
	}
	// Object-storage connections require credentials on creation, but an edit may
	// change endpoint metadata without re-entering secrets that are already stored.
	if input.Type == "object_storage" {
		if input.AccessKeyID == "" {
			input.AccessKeyID = string(current.Data["ACCESS_KEY_ID"])
		}
		if input.SecretAccessKey == "" {
			input.SecretAccessKey = string(current.Data["SECRET_ACCESS_KEY"])
		}
	}
	if err := validateExternalConnection(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret, err := s.connectionSecret(r.Context(), cluster, workspaceID, userID, current.Namespace, input, current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateSecretReferences(r.Context(), cluster, workspaceID, current.Namespace, current.Name, secret.Data); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	updated, err := cluster.client.CoreV1().Secrets(current.Namespace).Update(r.Context(), secret, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("update external connection secret: %v", err)
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadataFromConnectionSecret(updated))
}

func (s *connectionService) delete(w http.ResponseWriter, r *http.Request) {
	if !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	_, workspaceID, _, cluster, err := s.identity(r, true)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secret, err := s.getOwnedSecret(r.Context(), cluster, workspaceID, r.PathValue("id"))
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if err := validateSecretReferences(r.Context(), cluster, workspaceID, secret.Namespace, secret.Name, nil); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := cluster.client.CoreV1().Secrets(secret.Namespace).Delete(r.Context(), secret.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("delete external connection secret: %v", err)
		s.writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *connectionService) attachToDeployment(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.deployments.authorizedDeployment(w, r, "deployments:write")
	if !ok {
		return
	}
	var workspaceRole string
	if err := s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, principal.userID, principal.workspaceID).Scan(&workspaceRole); err != nil || (workspaceRole != "owner" && workspaceRole != "developer") {
		writeError(w, http.StatusForbidden, "Workspace developer or owner role required")
		return
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return
	}
	var input struct {
		ConnectionID string            `json:"connection_id"`
		EnvMapping   map[string]string `json:"env_mapping"`
	}
	if err := decodeJSON(w, r, &input); err != nil || input.ConnectionID == "" || len(input.EnvMapping) == 0 {
		writeError(w, http.StatusBadRequest, "A connection_id and env_mapping are required")
		return
	}
	provider, err := s.deployments.provisionerForContext(item.ClusterContext)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	cluster, ok := provider.(*kubernetesProvisioner)
	if !ok || cluster == nil {
		writeError(w, http.StatusServiceUnavailable, "Connection injection is unavailable for this deployment provider")
		return
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), principal.workspaceID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secret, err := s.getOwnedSecret(r.Context(), cluster, principal.workspaceID, input.ConnectionID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if secret.Namespace != namespace || secret.Annotations["crelan.io/cluster-context"] != item.ClusterContext {
		writeError(w, http.StatusConflict, "Connection and app must be in the same workspace cluster")
		return
	}
	for envName, secretKey := range input.EnvMapping {
		secretKey = canonicalConnectionSecretKey(secretKey)
		if !environmentNamePattern.MatchString(envName) || secretKey == "" {
			writeError(w, http.StatusBadRequest, "Environment names and secret field names are invalid")
			return
		}
		if _, exists := secret.Data[secretKey]; !exists {
			writeError(w, http.StatusBadRequest, "A mapped secret field was not found")
			return
		}
		input.EnvMapping[envName] = secretKey
	}
	deployments := cluster.client.AppsV1().Deployments(namespace)
	app, err := deployments.Get(r.Context(), item.Name, metav1.GetOptions{})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !ownsWorkspace(app.Labels, principal.workspaceID) || app.Labels[applicationLabel] != item.Name || len(app.Spec.Template.Spec.Containers) == 0 {
		writeError(w, http.StatusConflict, "App workload is not owned by this workspace")
		return
	}
	containerIndex := -1
	for i := range app.Spec.Template.Spec.Containers {
		if app.Spec.Template.Spec.Containers[i].Name == item.Name {
			containerIndex = i
			break
		}
	}
	if containerIndex < 0 {
		writeError(w, http.StatusConflict, "App container was not found")
		return
	}
	container := &app.Spec.Template.Spec.Containers[containerIndex]
	for envName, secretKey := range input.EnvMapping {
		valueFrom := &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name}, Key: secretKey}}
		found := false
		for i := range container.Env {
			if container.Env[i].Name == envName {
				container.Env[i] = corev1.EnvVar{Name: envName, ValueFrom: valueFrom}
				found = true
				break
			}
		}
		if !found {
			container.Env = append(container.Env, corev1.EnvVar{Name: envName, ValueFrom: valueFrom})
		}
	}
	_, err = deployments.Update(r.Context(), app, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("attach external connection to app: %v", err)
		s.writeFailure(w, err)
		return
	}
	if err := s.deployments.recordAudit(r.Context(), principal.workspaceID, principal.userID, "connection.attach", item.Name+":"+input.ConnectionID+":"+strings.Join(sortedMapKeys(input.EnvMapping), ",")); err != nil {
		log.Printf("audit connection attach: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "attached", "connection_id": input.ConnectionID, "environment_keys": sortedMapKeys(input.EnvMapping)})
}

func (s *connectionService) connectionSecret(ctx context.Context, cluster *kubernetesProvisioner, workspaceID, userID int64, namespace string, input externalConnectionInput, existing *corev1.Secret) (*corev1.Secret, error) {
	if existing != nil && input.EnvMapping == nil && existing.Annotations["crelan.io/env-mapping"] != "" {
		input.EnvMapping = make(map[string]string)
		if err := json.Unmarshal([]byte(existing.Annotations["crelan.io/env-mapping"]), &input.EnvMapping); err != nil {
			return nil, errors.New("existing connection mapping is invalid")
		}
	}
	values := map[string][]byte{}
	put := func(key, value string) {
		if value != "" {
			values[key] = []byte(value)
		}
	}
	put("CONNECTION_URL", input.ConnectionURL)
	put("ENDPOINT", input.Endpoint)
	put("HOST", input.Host)
	if input.Port > 0 {
		put("PORT", strconv.Itoa(input.Port))
	}
	put("DATABASE", input.Database)
	put("USERNAME", input.Username)
	put("PASSWORD", input.Password)
	put("BUCKET", input.Bucket)
	put("REGION", input.Region)
	put("ACCESS_KEY_ID", input.AccessKeyID)
	put("SECRET_ACCESS_KEY", input.SecretAccessKey)
	if existing != nil {
		for key, value := range existing.Data {
			if _, supplied := values[key]; !supplied {
				values[key] = append([]byte(nil), value...)
			}
		}
	}
	for envName, key := range input.EnvMapping {
		key = canonicalConnectionSecretKey(key)
		if !environmentNamePattern.MatchString(envName) || key == "" {
			return nil, errors.New("environment mapping is invalid")
		}
		if _, exists := values[key]; !exists {
			return nil, errors.New("environment mapping refers to a missing secret field")
		}
		input.EnvMapping[envName] = key
	}
	clusterContext := ""
	if s.deployments != nil {
		s.deployments.providerMu.RLock()
		clusterContext = s.deployments.activeContext
		s.deployments.providerMu.RUnlock()
	}
	endpoint := safeConnectionEndpoint(input)
	annotations := map[string]string{
		"crelan.io/connection-type":     input.Type,
		"crelan.io/connection-endpoint": endpoint,
		"crelan.io/connection-host":     input.Host,
		"crelan.io/connection-port":     strconv.Itoa(input.Port),
		"crelan.io/connection-database": input.Database,
		"crelan.io/connection-bucket":   input.Bucket,
		"crelan.io/connection-region":   input.Region,
		"crelan.io/cluster-context":     clusterContext,
		"crelan.io/updated-at":          time.Now().UTC().Format(time.RFC3339),
	}
	if input.EnvMapping != nil {
		encoded, err := json.Marshal(input.EnvMapping)
		if err != nil {
			return nil, err
		}
		annotations["crelan.io/env-mapping"] = string(encoded)
	}
	labels := map[string]string{
		externalConnectionLabel:     "true",
		workspaceLabel:              strconv.FormatInt(workspaceID, 10),
		"crelan.io/connection-name": input.Name,
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: externalConnectionSecretName(input.Name), Namespace: namespace, Labels: labels, Annotations: annotations}, Type: corev1.SecretTypeOpaque, Data: values}
	if existing != nil {
		if !ownsWorkspace(existing.Labels, workspaceID) || existing.Labels[externalConnectionLabel] != "true" {
			return nil, errors.New("connection secret is not owned by this workspace")
		}
		secret.ResourceVersion = existing.ResourceVersion
		secret.UID = existing.UID
		secret.CreationTimestamp = existing.CreationTimestamp
	}
	return secret, nil
}

func (s *connectionService) getOwnedSecret(ctx context.Context, cluster *kubernetesProvisioner, workspaceID int64, id string) (*corev1.Secret, error) {
	if id == "" || !deploymentNamePattern.MatchString(id) || len(id) > 49 {
		return nil, errors.New("invalid connection ID")
	}
	namespace, err := cluster.workspaceNamespace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	secret, err := cluster.client.CoreV1().Secrets(namespace).Get(ctx, externalConnectionSecretName(id), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	if !ownsWorkspace(secret.Labels, workspaceID) || secret.Labels[externalConnectionLabel] != "true" || secret.Labels["crelan.io/connection-name"] != id {
		return nil, sql.ErrNoRows
	}
	return secret, nil
}

func metadataFromConnectionSecret(secret *corev1.Secret) externalConnectionMetadata {
	metadata := externalConnectionMetadata{
		ID: secret.Labels["crelan.io/connection-name"], Name: secret.Labels["crelan.io/connection-name"],
		Type: secret.Annotations["crelan.io/connection-type"], Endpoint: secret.Annotations["crelan.io/connection-endpoint"],
		Host: secret.Annotations["crelan.io/connection-host"], Bucket: secret.Annotations["crelan.io/connection-bucket"],
		Region: secret.Annotations["crelan.io/connection-region"], SecretName: secret.Name,
		SourceServiceID: secret.Annotations["crelan.io/source-service-id"],
		ClusterContext:  secret.Annotations["crelan.io/cluster-context"], CreatedAt: secret.CreationTimestamp.UTC().Format(time.RFC3339), UpdatedAt: secret.Annotations["crelan.io/updated-at"],
		SecretKeys: make([]string, 0, len(secret.Data)),
	}
	metadata.Database = secret.Annotations["crelan.io/connection-database"]
	if raw := secret.Annotations["crelan.io/connection-port"]; raw != "" {
		metadata.Port, _ = strconv.Atoi(raw)
	}
	for key := range secret.Data {
		metadata.SecretKeys = append(metadata.SecretKeys, key)
	}
	if raw := secret.Annotations["crelan.io/env-mapping"]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &metadata.EnvMapping)
	}
	slices.Sort(metadata.SecretKeys)
	return metadata
}

func validateExternalConnection(input *externalConnectionInput) error {
	if input.Type == "" {
		input.Type = "generic"
	}
	switch input.Type {
	case "generic", "other", "postgresql", "mysql", "valkey", "redis", "rabbitmq", "object_storage":
	default:
		return errors.New("connection type must be postgresql, mysql, valkey, redis, rabbitmq, object_storage, or other")
	}
	if input.Port < 0 || input.Port > 65535 {
		return errors.New("connection port must be between 1 and 65535")
	}
	if input.ConnectionURL != "" {
		parsed, err := url.Parse(input.ConnectionURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Fragment != "" {
			return errors.New("connection_url must be an absolute URL without a fragment")
		}
	}
	if input.Endpoint != "" {
		parsed, err := url.Parse(input.Endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("endpoint must be an absolute URL without credentials, query, or fragment")
		}
	}
	if len(input.Endpoint) > 2048 || len(input.ConnectionURL) > 8192 || len(input.Host) > 253 {
		return errors.New("connection endpoint is too long")
	}
	if input.Host != "" && (strings.ContainsAny(input.Host, "/@?# \r\n\t") || net.ParseIP(input.Host) == nil && !validConnectionHostname(input.Host)) {
		return errors.New("host is invalid")
	}
	if input.Type == "object_storage" {
		if input.Endpoint == "" || input.Bucket == "" || input.AccessKeyID == "" || input.SecretAccessKey == "" {
			return errors.New("object_storage requires endpoint, bucket, access_key_id, and secret_access_key")
		}
		if len(input.Bucket) > 255 || strings.ContainsAny(input.Bucket, "/\x00\r\n") {
			return errors.New("bucket name is invalid")
		}
		endpoint, _ := url.Parse(input.Endpoint)
		if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
			return errors.New("object storage endpoint must use http or https")
		}
	} else if input.ConnectionURL == "" && input.Endpoint == "" && input.Host == "" {
		return errors.New("provide endpoint, host, or connection_url")
	}
	if input.Database != "" && (len(input.Database) > 128 || strings.ContainsAny(input.Database, "\x00\r\n")) {
		return errors.New("database name is invalid")
	}
	if input.Region != "" && (len(input.Region) > 128 || strings.ContainsAny(input.Region, "\x00\r\n")) {
		return errors.New("region is invalid")
	}
	for key, value := range map[string]string{"username": input.Username, "password": input.Password, "access_key_id": input.AccessKeyID, "secret_access_key": input.SecretAccessKey} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%s is too long or contains invalid characters", key)
		}
	}
	return nil
}

func validConnectionHostname(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || !customDomainLabelPattern.MatchString(strings.ToLower(label)) {
			return false
		}
	}
	return true
}

func safeConnectionEndpoint(input externalConnectionInput) string {
	if input.Endpoint != "" {
		return input.Endpoint
	}
	if input.ConnectionURL != "" {
		parsed, err := url.Parse(input.ConnectionURL)
		if err == nil {
			parsed.User = nil
			parsed.RawQuery = ""
			return parsed.String()
		}
	}
	if input.Host == "" {
		return ""
	}
	if input.Port > 0 {
		return net.JoinHostPort(input.Host, strconv.Itoa(input.Port))
	}
	return input.Host
}

func externalConnectionSecretName(id string) string { return "crelan-conn-" + id }

func validateSecretReferences(ctx context.Context, cluster *kubernetesProvisioner, workspaceID int64, namespace, secretName string, replacement map[string][]byte) error {
	apps, err := cluster.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: workspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
	if err != nil {
		return fmt.Errorf("check app connection references: %w", err)
	}
	for _, app := range apps.Items {
		if !ownsWorkspace(app.Labels, workspaceID) {
			continue
		}
		for _, container := range app.Spec.Template.Spec.Containers {
			for _, env := range container.Env {
				if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil || env.ValueFrom.SecretKeyRef.Name != secretName {
					continue
				}
				if replacement == nil {
					return fmt.Errorf("connection is attached to app %q; detach it before deleting", app.Name)
				}
				if _, exists := replacement[env.ValueFrom.SecretKeyRef.Key]; !exists {
					return fmt.Errorf("connection field %q is attached to app %q", env.ValueFrom.SecretKeyRef.Key, app.Name)
				}
			}
		}
	}
	return nil
}

func canonicalConnectionSecretKey(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "CONNECTION_URL", "ENDPOINT", "HOST", "PORT", "DATABASE", "USERNAME", "PASSWORD", "BUCKET", "REGION", "ACCESS_KEY_ID", "SECRET_ACCESS_KEY":
		return value
	default:
		return ""
	}
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (s *connectionService) writeFailure(w http.ResponseWriter, err error) {
	switch {
	case strings.Contains(err.Error(), "session"):
		writeError(w, http.StatusUnauthorized, "Sign in required")
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "Connection not found")
	case errors.Is(err, errKubernetesNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Kubernetes cluster is not connected", "code": "provisioner_unavailable"})
	case strings.Contains(err.Error(), "workspace role"):
		writeError(w, http.StatusForbidden, "Workspace developer or owner role required")
	case strings.Contains(err.Error(), "tenant") || strings.Contains(err.Error(), "namespace"):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadGateway, "Could not access workspace connections")
	}
}
