package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const registryCredentialLabel = "crelan.io/registry-credential"

var registryCredentialNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type registryCredentialService struct {
	auth        *authService
	deployments *deploymentService
}

type registryCredentialInput struct {
	Name     string `json:"name"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type registryCredential struct {
	Name   string `json:"name"`
	Server string `json:"server"`
}

func (s *registryCredentialService) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/registry-credentials", s.list)
	mux.HandleFunc("POST /api/v1/registry-credentials", s.create)
	mux.HandleFunc("DELETE /api/v1/registry-credentials/{name}", s.delete)
}

func (s *registryCredentialService) identity(r *http.Request) (int64, *kubernetesProvisioner, string, error) {
	_, workspaceID, err := s.auth.sessionWorkspace(r)
	if err != nil || workspaceID < 1 {
		return 0, nil, "", errors.New("sign in required")
	}
	provider, err := s.deployments.provisionerForContext("")
	if err != nil {
		return 0, nil, "", errKubernetesNotConfigured
	}
	provisioner, ok := provider.(*kubernetesProvisioner)
	if !ok {
		return 0, nil, "", errors.New("registry credentials are unavailable for the selected cluster")
	}
	namespace, err := provisioner.workspaceNamespace(r.Context(), workspaceID)
	if err != nil {
		return 0, nil, "", err
	}
	return workspaceID, provisioner, namespace, nil
}

func (s *registryCredentialService) authorizeWrite(w http.ResponseWriter, r *http.Request, workspaceID int64) bool {
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

func (s *registryCredentialService) list(w http.ResponseWriter, r *http.Request) {
	workspaceID, provisioner, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	secrets, err := provisioner.client.CoreV1().Secrets(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: workspaceLabel + "=" + fmt.Sprint(workspaceID) + "," + registryCredentialLabel + "=true"})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	items := make([]registryCredential, 0, len(secrets.Items))
	for _, secret := range secrets.Items {
		items = append(items, registryCredential{Name: secret.Name, Server: secret.Annotations["crelan.io/registry-server"]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": items})
}

func (s *registryCredentialService) create(w http.ResponseWriter, r *http.Request) {
	workspaceID, provisioner, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	var input registryCredentialInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "Enter a valid registry credential")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Server = strings.TrimSpace(input.Server)
	input.Username = strings.TrimSpace(input.Username)
	if !registryCredentialNamePattern.MatchString(input.Name) || len(input.Username) < 1 || len(input.Username) > 256 || len(input.Password) < 1 || len(input.Password) > 4096 {
		writeError(w, http.StatusBadRequest, "Credential name, username, or password is invalid")
		return
	}
	parsed, err := url.Parse("https://" + input.Server)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(input.Server, "\r\n") {
		writeError(w, http.StatusBadRequest, "Registry server must be a host such as ghcr.io or registry.example.com:5000")
		return
	}
	config, err := json.Marshal(map[string]any{"auths": map[string]any{
		input.Server: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(input.Username + ":" + input.Password))},
	}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not encode registry credential")
		return
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: input.Name, Namespace: namespace,
			Labels:      map[string]string{workspaceLabel: fmt.Sprint(workspaceID), registryCredentialLabel: "true", "app.kubernetes.io/managed-by": "crelan"},
			Annotations: map[string]string{"crelan.io/registry-server": input.Server},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: config},
	}
	if _, err := provisioner.client.CoreV1().Secrets(namespace).Create(r.Context(), secret, metav1.CreateOptions{}); err != nil {
		s.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, registryCredential{Name: input.Name, Server: input.Server})
}

func (s *registryCredentialService) delete(w http.ResponseWriter, r *http.Request) {
	workspaceID, provisioner, namespace, err := s.identity(r)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if !s.authorizeWrite(w, r, workspaceID) {
		return
	}
	name := r.PathValue("name")
	secret, err := provisioner.client.CoreV1().Secrets(namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil || secret.Labels[workspaceLabel] != fmt.Sprint(workspaceID) || secret.Labels[registryCredentialLabel] != "true" {
		writeError(w, http.StatusNotFound, "Registry credential not found")
		return
	}
	apps, err := provisioner.client.AppsV1().Deployments(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: workspaceLabel + "=" + fmt.Sprint(workspaceID)})
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	if deploymentUsesPullSecret(apps.Items, name) {
		writeError(w, http.StatusConflict, "This credential is used by an application. Update or delete that application first.")
		return
	}
	if err := provisioner.client.CoreV1().Secrets(namespace).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		s.writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deploymentUsesPullSecret(deployments []appsv1.Deployment, secretName string) bool {
	for _, item := range deployments {
		for _, secret := range item.Spec.Template.Spec.ImagePullSecrets {
			if secret.Name == secretName {
				return true
			}
		}
	}
	return false
}

func (s *registryCredentialService) writeFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errKubernetesNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "Kubernetes image registry provisioning is not configured")
	case strings.Contains(err.Error(), "sign in required"):
		writeError(w, http.StatusUnauthorized, "Sign in required")
	case apierrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "A registry credential with that name already exists")
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusServiceUnavailable, "The platform Kubernetes identity cannot manage registry credentials in this workspace")
	default:
		writeError(w, http.StatusInternalServerError, "Registry credential operation failed")
	}
}
