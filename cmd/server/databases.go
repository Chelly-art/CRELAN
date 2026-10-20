package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// databaseService exposes a deliberately small database API. Database
// resources are created only after resolving the caller's workspace to the
// namespace assigned to its Capsule tenant.
type databaseService struct {
	auth        *authService
	provisioner *kubernetesProvisioner
}

var databaseNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$`)

var (
	cloudNativePGGVR                = k8sschema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}
	cloudNativePGScheduledBackupGVR = k8sschema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "scheduledbackups"}
	mysqlInnoDBGVR                  = k8sschema.GroupVersionResource{Group: "mysql.oracle.com", Version: "v2", Resource: "innodbclusters"}
)

type databaseEngine struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type databaseRecord struct {
	Name           string `json:"name"`
	Engine         string `json:"engine"`
	Version        string `json:"version"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
	Endpoint       string `json:"endpoint"`
	Port           int    `json:"port"`
	Storage        string `json:"storage"`
	Namespace      string `json:"namespace"`
	CreatedAt      string `json:"created_at,omitempty"`
	BackupsEnabled bool   `json:"backups_enabled"`
}

type createDatabaseInput struct {
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Version string `json:"version,omitempty"`
	Storage string `json:"storage,omitempty"`
}

func (s *databaseService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/database-engines", s.engines)
	mux.HandleFunc("GET /api/v1/databases", s.list)
	mux.HandleFunc("POST /api/v1/databases", s.create)
	mux.HandleFunc("GET /api/v1/databases/{name}", s.get)
	mux.HandleFunc("DELETE /api/v1/databases/{name}", s.delete)
}

func (s *databaseService) identity(r *http.Request) (int64, string, error) {
	if s.auth == nil {
		return 0, "", errors.New("authentication is not configured")
	}
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil || workspaceID < 1 {
		return 0, "", errors.New("sign in required")
	}
	if s.provisioner == nil {
		return workspaceID, "", errKubernetesNotConfigured
	}
	namespace, err := s.provisioner.workspaceNamespace(r.Context(), workspaceID)
	return workspaceID, namespace, err
}

func (s *databaseService) engines(w http.ResponseWriter, r *http.Request) {
	_, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	engines := []databaseEngine{
		{ID: "postgresql", Name: "PostgreSQL", Description: "PostgreSQL managed by CloudNativePG", Available: true},
		{ID: "mysql", Name: "MySQL", Description: "MySQL InnoDB Cluster managed by the Oracle MySQL Operator", Available: true},
	}
	ctx := r.Context()
	for i := range engines {
		gvr := databaseGVR(engines[i].ID)
		_, err := s.provisioner.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1})
		engines[i].Available = err == nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"engines": engines})
}

func (s *databaseService) list(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	items := make([]databaseRecord, 0)
	for _, engine := range []string{"postgresql", "mysql"} {
		list, err := s.provisioner.dynamicClient.Resource(databaseGVR(engine)).Namespace(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: databaseWorkspaceLabel + "=" + fmt.Sprint(workspaceID)})
		if err != nil && !isDatabaseOperatorMissing(err) {
			s.writeFailure(w, err)
			return
		}
		for i := range list.Items {
			items = append(items, databaseRecordFromResource(&list.Items[i], engine, namespace))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": items})
}

func (s *databaseService) get(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	name := r.PathValue("name")
	resource, engine, err := s.findOwned(r.Context(), namespace, workspaceID, name)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, databaseRecordFromResource(resource, engine, namespace))
}

func (s *databaseService) create(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	var input createDatabaseInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Provide a valid database name and engine")
		return
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	input.Engine = strings.ToLower(strings.TrimSpace(input.Engine))
	if !databaseNamePattern.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, "Database name must be a lowercase DNS name of at most 48 characters")
		return
	}
	if input.Engine != "postgresql" && input.Engine != "mysql" {
		writeError(w, http.StatusBadRequest, "Supported engines are postgresql and mysql")
		return
	}
	if len(input.Name) > 40 {
		writeError(w, http.StatusBadRequest, "Database name must be 40 characters or fewer")
		return
	}
	version, err := databaseVersion(input.Engine, input.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	storage, err := databaseStorage(input.Storage)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.checkDatabaseLimits(w, r, workspaceID, namespace, storage) {
		return
	}
	if err := s.ensureDatabaseQuota(r.Context(), workspaceID, namespace); err != nil {
		log.Printf("apply workspace database quota: %v", err)
		writeError(w, http.StatusServiceUnavailable, "Could not apply workspace database limits")
		return
	}
	secretName := "db-" + input.Name + "-credentials"
	password, err := randomDatabasePassword()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not generate database credentials")
		return
	}
	labels := map[string]string{databaseWorkspaceLabel: fmt.Sprint(workspaceID), databaseNameLabel: input.Name, "app.kubernetes.io/managed-by": "crelan"}
	secretData := map[string][]byte{}
	if input.Engine == "postgresql" {
		secretData = map[string][]byte{"username": []byte("app"), "password": []byte(password)}
	} else {
		rootPassword, passwordErr := randomDatabasePassword()
		if passwordErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not generate database credentials")
			return
		}
		secretData = map[string][]byte{"rootUser": []byte("root"), "rootHost": []byte("%"), "rootPassword": []byte(rootPassword), "appUser": []byte("app"), "appPassword": []byte(password)}
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace, Labels: labels}, Type: corev1.SecretTypeOpaque, Data: secretData}
	createdSecret, err := s.provisioner.client.CoreV1().Secrets(namespace).Create(r.Context(), secret, metav1.CreateOptions{})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	backupSecretName, backupConfigured, backupErr := s.ensureBackupSecret(r.Context(), namespace, input.Name, workspaceID)
	if backupErr != nil {
		_ = s.provisioner.client.CoreV1().Secrets(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		writeError(w, http.StatusServiceUnavailable, "Could not configure database backup credentials")
		return
	}
	resource := databaseCustomResource(input, version, storage, namespace, secretName, labels)
	configureDatabaseBackups(resource, input.Engine, backupSecretName, backupConfigured)
	created, err := s.provisioner.dynamicClient.Resource(databaseGVR(input.Engine)).Namespace(namespace).Create(r.Context(), resource, metav1.CreateOptions{})
	if err != nil {
		_ = s.provisioner.client.CoreV1().Secrets(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		s.writeFailure(w, err)
		return
	}
	if input.Engine == "postgresql" && backupConfigured {
		if err := s.createPostgresBackupSchedule(r.Context(), namespace, input.Name, workspaceID); err != nil {
			log.Printf("create PostgreSQL backup schedule: %v", err)
			_ = s.provisioner.dynamicClient.Resource(cloudNativePGGVR).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
			writeError(w, http.StatusServiceUnavailable, "Could not configure the PostgreSQL backup schedule")
			return
		}
	}
	if err := s.ensureDatabaseNetworkPolicy(r.Context(), namespace, input.Name, workspaceID); err != nil {
		_ = s.provisioner.dynamicClient.Resource(databaseGVR(input.Engine)).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
		_ = s.provisioner.client.CoreV1().Secrets(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		writeError(w, http.StatusServiceUnavailable, "Could not apply database network isolation")
		return
	}
	if input.Engine == "mysql" {
		if err := s.createMySQLAppUserJob(r.Context(), namespace, input.Name, secretName, labels); err != nil {
			log.Printf("create MySQL application-user job: %v", err)
			_ = s.provisioner.dynamicClient.Resource(databaseGVR(input.Engine)).Namespace(namespace).Delete(context.Background(), input.Name, metav1.DeleteOptions{})
			_ = s.provisioner.client.CoreV1().Secrets(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
			writeError(w, http.StatusServiceUnavailable, "Could not initialize the MySQL application user")
			return
		}
	}
	gv, parseErr := k8sschema.ParseGroupVersion(created.GetAPIVersion())
	if parseErr == nil {
		owner := metav1.NewControllerRef(created, gv.WithKind(created.GetKind()))
		createdSecret.OwnerReferences = []metav1.OwnerReference{*owner}
		_, _ = s.provisioner.client.CoreV1().Secrets(namespace).Update(r.Context(), createdSecret, metav1.UpdateOptions{})
		if backupConfigured {
			backupSecret, getErr := s.provisioner.client.CoreV1().Secrets(namespace).Get(r.Context(), backupSecretName, metav1.GetOptions{})
			if getErr == nil {
				backupSecret.OwnerReferences = []metav1.OwnerReference{*owner}
				_, _ = s.provisioner.client.CoreV1().Secrets(namespace).Update(r.Context(), backupSecret, metav1.UpdateOptions{})
			}
		}
	}
	record := databaseRecordFromResource(created, input.Engine, namespace)
	record.BackupsEnabled = backupConfigured
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, action, details, created_at) VALUES (?, 'database.create', ?, ?)`, workspaceID, input.Name+" ("+input.Engine+")", time.Now().UTC().Format(time.RFC3339Nano))
	response := map[string]any{"database": record, "credentials": map[string]string{"username": "app", "password": password, "database": "app"}}
	writeJSON(w, http.StatusCreated, response)
}

func (s *databaseService) checkDatabaseLimits(w http.ResponseWriter, r *http.Request, workspaceID int64, namespace, requestedStorage string) bool {
	const maxDatabases = 5
	const maxStorageGi = 100
	const maxWorkspaceStorageGi = 500
	requestedGi := resourceGi(requestedStorage)
	if requestedGi > maxStorageGi {
		writeError(w, http.StatusBadRequest, "Database storage must be between 1Mi and 100Gi")
		return false
	}
	usedStorage := requestedGi
	count := 0
	for _, engine := range []string{"postgresql", "mysql"} {
		list, err := s.provisioner.dynamicClient.Resource(databaseGVR(engine)).Namespace(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: databaseWorkspaceLabel + "=" + strconv.FormatInt(workspaceID, 10)})
		if err != nil && !isDatabaseOperatorMissing(err) {
			s.writeFailure(w, err)
			return false
		}
		if err == nil {
			count += len(list.Items)
			for i := range list.Items {
				usedStorage += resourceGi(databaseResourceStorage(&list.Items[i], engine))
			}
		}
	}
	if count >= maxDatabases {
		writeError(w, http.StatusTooManyRequests, "Workspace database limit reached (5 databases)")
		return false
	}
	if usedStorage > maxWorkspaceStorageGi {
		writeError(w, http.StatusTooManyRequests, "Workspace allocated database storage limit exceeded (500Gi)")
		return false
	}
	return true
}

func resourceGi(value string) int64 {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "Ti") {
		v, err := strconv.ParseInt(strings.TrimSuffix(value, "Ti"), 10, 64)
		if err != nil || v <= 0 || v > (1<<62)/1024 {
			return 1 << 62
		}
		return v * 1024
	}
	if strings.HasSuffix(value, "Gi") {
		v, err := strconv.ParseInt(strings.TrimSuffix(value, "Gi"), 10, 64)
		if err != nil || v <= 0 || v > (1<<63)-1024 {
			return 1 << 62
		}
		return v
	}
	if strings.HasSuffix(value, "Mi") {
		v, err := strconv.ParseInt(strings.TrimSuffix(value, "Mi"), 10, 64)
		if err != nil || v <= 0 || v > (1<<63)-1024 {
			return 1 << 62
		}
		return (v + 1023) / 1024
	}
	return 1 << 62
}

func (s *databaseService) ensureDatabaseQuota(ctx context.Context, workspaceID int64, namespace string) error {
	labels := map[string]string{databaseWorkspaceLabel: strconv.FormatInt(workspaceID, 10)}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "crelan-database-limits", Namespace: namespace, Labels: labels}, Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
		corev1.ResourceRequestsStorage:                      resource.MustParse("1500Gi"),
		corev1.ResourceName("count/persistentvolumeclaims"): resource.MustParse("15"),
		corev1.ResourceName("count/pods"):                   resource.MustParse("50"),
	}}}
	quotas := s.provisioner.client.CoreV1().ResourceQuotas(namespace)
	current, err := quotas.Get(ctx, quota.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = quotas.Create(ctx, quota, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.Labels[databaseWorkspaceLabel] != labels[databaseWorkspaceLabel] {
		return errors.New("database quota is owned by another workspace")
	}
	quota.ResourceVersion = current.ResourceVersion
	_, err = quotas.Update(ctx, quota, metav1.UpdateOptions{})
	return err
}

func (s *databaseService) ensureBackupSecret(ctx context.Context, namespace, database string, workspaceID int64) (string, bool, error) {
	bucket := strings.TrimSpace(os.Getenv("CRELAN_DB_BACKUP_BUCKET"))
	accessKey := os.Getenv("CRELAN_DB_BACKUP_ACCESS_KEY")
	secretKeyValue := os.Getenv("CRELAN_DB_BACKUP_SECRET_KEY")
	region := strings.TrimSpace(os.Getenv("CRELAN_DB_BACKUP_REGION"))
	if bucket == "" || accessKey == "" || secretKeyValue == "" || region == "" {
		return "", false, nil
	}
	name := "db-" + database + "-backup"
	labels := map[string]string{databaseWorkspaceLabel: strconv.FormatInt(workspaceID, 10), "app.kubernetes.io/managed-by": "crelan"}
	data := map[string][]byte{
		"ACCESS_KEY_ID": []byte(accessKey), "ACCESS_SECRET_KEY": []byte(secretKeyValue),
		"aws_access_key_id": []byte(accessKey), "aws_secret_access_key": []byte(secretKeyValue),
		"awsAccessKeyId": []byte(accessKey), "awsSecretAccessKey": []byte(secretKeyValue),
		"bucket": []byte(bucket), "region": []byte(region),
	}
	if endpoint := strings.TrimSpace(os.Getenv("CRELAN_DB_BACKUP_ENDPOINT")); endpoint != "" {
		data["s3EndpointOverride"] = []byte(endpoint)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Type: corev1.SecretTypeOpaque, Data: data}
	_, err := s.provisioner.client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return name, err == nil, err
}

func configureDatabaseBackups(resource *unstructured.Unstructured, engine, secretName string, enabled bool) {
	if !enabled {
		return
	}
	bucket := strings.TrimSpace(os.Getenv("CRELAN_DB_BACKUP_BUCKET"))
	endpoint := strings.TrimSpace(os.Getenv("CRELAN_DB_BACKUP_ENDPOINT"))
	if engine == "postgresql" {
		store := map[string]any{
			"destinationPath": "s3://" + bucket + "/crelan/" + resource.GetName(),
			"s3Credentials": map[string]any{
				"accessKeyId":     map[string]any{"name": secretName, "key": "ACCESS_KEY_ID"},
				"secretAccessKey": map[string]any{"name": secretName, "key": "ACCESS_SECRET_KEY"},
			},
			"wal": map[string]any{"compression": "gzip"},
		}
		if endpoint != "" {
			store["endpointURL"] = endpoint
		}
		_ = unstructured.SetNestedMap(resource.Object, map[string]any{"barmanObjectStore": store, "retentionPolicy": "30d"}, "spec", "backup")
		return
	}
	storage := map[string]any{"bucketName": bucket, "config": secretName, "prefix": "crelan/" + resource.GetName()}
	if endpoint != "" {
		storage["endpoint"] = endpoint
	}
	profile := map[string]any{"name": "daily", "dumpInstance": map[string]any{"storage": map[string]any{"s3": storage}}}
	schedule := map[string]any{"name": "daily", "schedule": "0 2 * * *", "backupProfileName": "daily", "enabled": true}
	_ = unstructured.SetNestedSlice(resource.Object, []any{profile}, "spec", "backupProfiles")
	_ = unstructured.SetNestedSlice(resource.Object, []any{schedule}, "spec", "backupSchedules")
}

func (s *databaseService) createPostgresBackupSchedule(ctx context.Context, namespace, name string, workspaceID int64) error {
	backup := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1", "kind": "ScheduledBackup",
		"metadata": map[string]any{"name": name + "-daily", "namespace": namespace, "labels": map[string]any{databaseWorkspaceLabel: strconv.FormatInt(workspaceID, 10)}},
		"spec":     map[string]any{"schedule": "0 0 2 * * *", "backupOwnerReference": "cluster", "immediate": true, "cluster": map[string]any{"name": name}},
	}}
	_, err := s.provisioner.dynamicClient.Resource(cloudNativePGScheduledBackupGVR).Namespace(namespace).Create(ctx, backup, metav1.CreateOptions{})
	return err
}

func (s *databaseService) ensureDatabaseNetworkPolicy(ctx context.Context, namespace, name string, workspaceID int64) error {
	match := map[string]string{"cnpg.io/cluster": name}
	if _, err := s.provisioner.dynamicClient.Resource(cloudNativePGGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		match = map[string]string{"mysql.oracle.com/cluster": name}
	} else if err != nil {
		return err
	}
	operatorNamespaces := []string{strings.TrimSpace(os.Getenv("CRELAN_CLOUDNATIVEPG_NAMESPACE")), strings.TrimSpace(os.Getenv("CRELAN_MYSQL_OPERATOR_NAMESPACE"))}
	if operatorNamespaces[0] == "" {
		operatorNamespaces[0] = "cnpg-system"
	}
	if operatorNamespaces[1] == "" {
		operatorNamespaces[1] = "mysql-operator"
	}
	peers := []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}
	for _, operatorNamespace := range operatorNamespaces {
		peers = append(peers, networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": operatorNamespace}}})
	}
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "crelan-db-" + name + "-ingress", Namespace: namespace, Labels: map[string]string{databaseWorkspaceLabel: strconv.FormatInt(workspaceID, 10)}},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: match},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: peers}},
		},
	}
	policies := s.provisioner.client.NetworkingV1().NetworkPolicies(namespace)
	current, err := policies.Get(ctx, policy.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = policies.Create(ctx, policy, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	policy.ResourceVersion = current.ResourceVersion
	_, err = policies.Update(ctx, policy, metav1.UpdateOptions{})
	return err
}

func (s *databaseService) createMySQLAppUserJob(ctx context.Context, namespace, database, secret string, labels map[string]string) error {
	backoff := int32(3)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "db-" + database + "-init-user", Namespace: namespace, Labels: labels},
		Spec: batchv1.JobSpec{BackoffLimit: &backoff, TTLSecondsAfterFinished: ptr(int32(86400)), Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyOnFailure, Containers: []corev1.Container{{
				Name: "initialize-app-user", Image: "mysql:8.4", Command: []string{"/bin/sh", "-ec"},
				Args: []string{`for attempt in $(seq 1 180); do if mysql -h "$DB_HOST" -u "$ROOT_USER" -e 'SELECT 1' >/dev/null 2>&1; then break; fi; sleep 5; done; mysql -h "$DB_HOST" -u "$ROOT_USER" -e "CREATE DATABASE IF NOT EXISTS app; CREATE USER IF NOT EXISTS 'app'@'%' IDENTIFIED BY '${APP_PASSWORD}'; ALTER USER 'app'@'%' IDENTIFIED BY '${APP_PASSWORD}'; GRANT ALL PRIVILEGES ON app.* TO 'app'@'%';"`},
				Env: []corev1.EnvVar{
					{Name: "DB_HOST", Value: database + "." + namespace + ".svc.cluster.local"},
					{Name: "ROOT_USER", ValueFrom: secretKey(secret, "rootUser")},
					{Name: "MYSQL_PWD", ValueFrom: secretKey(secret, "rootPassword")},
					{Name: "APP_PASSWORD", ValueFrom: secretKey(secret, "appPassword")},
				},
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), RunAsNonRoot: ptr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
			}}, SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(999)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}},
		}},
	}
	_, err := s.provisioner.client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	return err
}

func secretKey(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func (s *databaseService) delete(w http.ResponseWriter, r *http.Request) {
	workspaceID, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	name := r.PathValue("name")
	resource, engine, err := s.findOwned(r.Context(), namespace, workspaceID, name)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	uid := resource.GetUID()
	err = s.provisioner.dynamicClient.Resource(databaseGVR(engine)).Namespace(namespace).Delete(r.Context(), name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if engine == "postgresql" {
		_ = s.provisioner.dynamicClient.Resource(cloudNativePGScheduledBackupGVR).Namespace(namespace).Delete(r.Context(), name+"-daily", metav1.DeleteOptions{})
	}
	_ = s.provisioner.client.NetworkingV1().NetworkPolicies(namespace).Delete(r.Context(), "crelan-db-"+name+"-ingress", metav1.DeleteOptions{})
	_ = s.provisioner.client.BatchV1().Jobs(namespace).Delete(r.Context(), "db-"+name+"-init-user", metav1.DeleteOptions{})
	_, _ = s.auth.db.ExecContext(r.Context(), `INSERT INTO audit_events (workspace_id, action, details, created_at) VALUES (?, 'database.delete', ?, ?)`, workspaceID, name+" ("+engine+")", time.Now().UTC().Format(time.RFC3339Nano))
	// The operator may still need this Secret during finalization. Remove it
	// only after the custom resource has disappeared (normally the operator
	// owns finalization); a garbage-collection owner reference is not portable.
	w.WriteHeader(http.StatusNoContent)
}

func (s *databaseService) authorizeWrite(w http.ResponseWriter, r *http.Request, workspaceID int64) bool {
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

func (s *databaseService) findOwned(ctx context.Context, namespace string, workspaceID int64, name string) (*unstructured.Unstructured, string, error) {
	if !databaseNamePattern.MatchString(name) {
		return nil, "", apierrors.NewNotFound(k8sschema.GroupResource{Resource: "databases"}, name)
	}
	for _, engine := range []string{"postgresql", "mysql"} {
		resource, err := s.provisioner.dynamicClient.Resource(databaseGVR(engine)).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if (apierrors.IsNotFound(err) && !isDatabaseOperatorMissing(err)) || isDatabaseOperatorMissing(err) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if resource.GetLabels()[databaseWorkspaceLabel] == fmt.Sprint(workspaceID) && resource.GetLabels()[databaseNameLabel] == name {
			return resource, engine, nil
		}
	}
	return nil, "", apierrors.NewNotFound(k8sschema.GroupResource{Resource: "databases"}, name)
}

const (
	databaseWorkspaceLabel = "crelan.io/workspace-id"
	databaseNameLabel      = "crelan.io/database-name"
)

func databaseGVR(engine string) k8sschema.GroupVersionResource {
	if engine == "mysql" {
		return mysqlInnoDBGVR
	}
	return cloudNativePGGVR
}

func databaseVersion(engine, requested string) (string, error) {
	configured := strings.TrimSpace(os.Getenv("CRELAN_DB_POSTGRES_VERSION"))
	if engine == "mysql" {
		configured = strings.TrimSpace(os.Getenv("CRELAN_DB_MYSQL_VERSION"))
	}
	if requested == "" {
		if configured != "" {
			return configured, nil
		}
		if engine == "postgresql" {
			return "16", nil
		}
		return "8.4.4", nil
	}
	if !regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`).MatchString(requested) {
		return "", errors.New("version must contain only numeric components")
	}
	if configured == "" || requested != configured {
		return "", errors.New("requested database version is not enabled by the platform")
	}
	return requested, nil
}

func databaseStorage(value string) (string, error) {
	if value == "" {
		return "10Gi", nil
	}
	if len(value) > 16 || !regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi|Ti)$`).MatchString(value) {
		return "", errors.New("storage must be a positive quantity such as 10Gi")
	}
	return value, nil
}

func randomDatabasePassword() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func databaseCustomResource(input createDatabaseInput, version, storage, namespace, secret string, labels map[string]string) *unstructured.Unstructured {
	metadata := map[string]any{"name": input.Name, "namespace": namespace, "labels": stringMapAny(labels)}
	var spec map[string]any
	if input.Engine == "postgresql" {
		spec = map[string]any{
			"instances": int64(3), "imageName": "ghcr.io/cloudnative-pg/postgresql:" + version,
			"bootstrap": map[string]any{"initdb": map[string]any{"database": "app", "owner": "app", "secret": map[string]any{"name": secret}}},
			"storage":   map[string]any{"size": storage},
			"affinity": map[string]any{
				"enablePodAntiAffinity": true,
				"topologyKey":           "topology.kubernetes.io/zone",
				"podAntiAffinityType":   "preferred",
			},
		}
	} else {
		spec = map[string]any{
			"secretName": secret, "instances": int64(3), "version": version,
			"router": map[string]any{"instances": int64(1)}, "tlsUseSelfSigned": true,
			"datadirVolumeClaimTemplate": map[string]any{"accessModes": []any{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]any{"storage": storage}}},
		}
	}
	apiVersion, kind := "postgresql.cnpg.io/v1", "Cluster"
	if input.Engine == "mysql" {
		apiVersion, kind = "mysql.oracle.com/v2", "InnoDBCluster"
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": metadata, "spec": spec}}
}

func stringMapAny(input map[string]string) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func databaseRecordFromResource(resource *unstructured.Unstructured, engine, namespace string) databaseRecord {
	status, _, _ := unstructured.NestedString(resource.Object, "status", "phase")
	message, _, _ := unstructured.NestedString(resource.Object, "status", "message")
	if status == "" && engine == "mysql" {
		status, _, _ = unstructured.NestedString(resource.Object, "status", "status")
	}
	if status == "" {
		if conditions, found, _ := unstructured.NestedSlice(resource.Object, "status", "conditions"); found {
			for _, item := range conditions {
				condition, ok := item.(map[string]any)
				if !ok || condition["type"] != "Ready" {
					continue
				}
				if condition["status"] == "True" {
					status = "ready"
				} else if condition["status"] == "False" {
					status = "degraded"
				}
				if reason, ok := condition["reason"].(string); ok && reason != "" {
					message = reason
				}
				break
			}
		}
	}
	if status == "" {
		status = "provisioning"
	}
	version, _, _ := unstructured.NestedString(resource.Object, "spec", "imageName")
	if engine == "mysql" {
		version, _, _ = unstructured.NestedString(resource.Object, "spec", "version")
	}
	if engine == "postgresql" {
		version = strings.TrimPrefix(version, "ghcr.io/cloudnative-pg/postgresql:")
	}
	endpoint, port := resource.GetName()+"-rw."+namespace+".svc.cluster.local", 5432
	if engine == "mysql" {
		endpoint, port = resource.GetName()+"."+namespace+".svc.cluster.local", 3306
	}
	backupsEnabled := false
	if engine == "postgresql" {
		_, backupsEnabled, _ = unstructured.NestedMap(resource.Object, "spec", "backup", "barmanObjectStore")
	} else {
		profiles, found, _ := unstructured.NestedSlice(resource.Object, "spec", "backupProfiles")
		backupsEnabled = found && len(profiles) > 0
	}
	return databaseRecord{Name: resource.GetName(), Engine: engine, Version: version, Status: status, Message: message, Endpoint: endpoint, Port: port, Storage: databaseResourceStorage(resource, engine), Namespace: namespace, CreatedAt: resource.GetCreationTimestamp().UTC().Format("2006-01-02T15:04:05Z07:00"), BackupsEnabled: backupsEnabled}
}

func databaseResourceStorage(resource *unstructured.Unstructured, engine string) string {
	path := []string{"spec", "storage", "size"}
	if engine == "mysql" {
		path = []string{"spec", "datadirVolumeClaimTemplate", "resources", "requests", "storage"}
	}
	value, _, _ := unstructured.NestedString(resource.Object, path...)
	return value
}

func isDatabaseOperatorMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "the server could not find the requested resource") || strings.Contains(message, "no matches for kind")
}

func (s *databaseService) writeFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errKubernetesNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "Kubernetes database provisioning is not configured")
	case strings.Contains(err.Error(), "sign in required"):
		writeError(w, http.StatusUnauthorized, "Sign in required")
	case strings.Contains(err.Error(), "has not been provisioned"), strings.Contains(err.Error(), "not assigned to its expected Capsule tenant"):
		writeError(w, http.StatusConflict, err.Error())
	case isDatabaseOperatorMissing(err):
		writeError(w, http.StatusServiceUnavailable, "The database operator or its CRD is not installed for this engine")
	case apierrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "Database not found")
	case apierrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "A database with that name already exists")
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusServiceUnavailable, "The platform Kubernetes identity cannot provision databases in this tenant")
	default:
		writeError(w, http.StatusInternalServerError, "Database operation failed")
	}
}
