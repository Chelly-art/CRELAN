package main

import (
	"log"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type retainedAppVolume struct {
	Name         string    `json:"name"`
	Application  string    `json:"application"`
	Size         string    `json:"size"`
	StorageClass string    `json:"storage_class,omitempty"`
	AccessMode   string    `json:"access_mode"`
	MountPath    string    `json:"mount_path,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	Orphaned     bool      `json:"orphaned"`
}

func (s *deploymentService) registerStorageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workspace/storage", s.listRetainedAppVolumes)
	mux.HandleFunc("DELETE /api/v1/workspace/storage/{app}", s.deleteRetainedAppVolume)
}

func (s *deploymentService) listRetainedAppVolumes(w http.ResponseWriter, r *http.Request) {
	principal, cluster, namespace, ok := s.storagePrincipal(w, r, "deployments:read", false)
	if !ok {
		return
	}
	claims, err := cluster.client.CoreV1().PersistentVolumeClaims(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: workspaceLabel + "=" + strconv.FormatInt(principal.workspaceID, 10)})
	if err != nil {
		log.Printf("list workspace retained app volumes: %v", err)
		writeError(w, http.StatusBadGateway, "Could not list workspace app volumes")
		return
	}
	items := make([]retainedAppVolume, 0, len(claims.Items))
	for i := range claims.Items {
		claim := &claims.Items[i]
		appName := claim.Labels[applicationLabel]
		if !ownsWorkspace(claim.Labels, principal.workspaceID) || appName == "" {
			continue
		}
		deployed := false
		_, getErr := cluster.client.AppsV1().Deployments(namespace).Get(r.Context(), appName, metav1.GetOptions{})
		if getErr == nil {
			deployed = true
		} else if getErr != nil && !apierrors.IsNotFound(getErr) {
			log.Printf("check retained volume app %s: %v", appName, getErr)
			writeError(w, http.StatusBadGateway, "Could not determine which workspace volumes are retained")
			return
		}
		item := retainedAppVolume{
			Name: claim.Name, Application: appName, StorageClass: "", AccessMode: claimAccessMode(claim),
			MountPath: claim.Annotations["crelan.io/storage-mount-path"], Status: string(claim.Status.Phase),
			CreatedAt: claim.CreationTimestamp.Time.UTC(), Orphaned: !deployed,
		}
		if claim.Spec.StorageClassName != nil {
			item.StorageClass = *claim.Spec.StorageClassName
		}
		if mode := claim.Annotations["crelan.io/storage-access-mode"]; mode != "" {
			item.AccessMode = mode
		}
		if size := claim.Spec.Resources.Requests[corev1.ResourceStorage]; !size.IsZero() {
			item.Size = size.String()
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": items})
}

func (s *deploymentService) deleteRetainedAppVolume(w http.ResponseWriter, r *http.Request) {
	principal, cluster, namespace, ok := s.storagePrincipal(w, r, "deployments:write", true)
	if !ok {
		return
	}
	if r.URL.Query().Get("confirm_data_loss") != "true" {
		writeError(w, http.StatusConflict, "This permanently deletes retained app data. Repeat with ?confirm_data_loss=true to confirm")
		return
	}
	appName := r.PathValue("app")
	if !deploymentNamePattern.MatchString(appName) {
		writeError(w, http.StatusNotFound, "Retained volume not found")
		return
	}
	claimName := appDataPVCName(appName)
	claims := cluster.client.CoreV1().PersistentVolumeClaims(namespace)
	claim, err := claims.Get(r.Context(), claimName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "Retained volume not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not load retained volume")
		return
	}
	if !ownsWorkspace(claim.Labels, principal.workspaceID) || claim.Labels[applicationLabel] != appName {
		writeError(w, http.StatusNotFound, "Retained volume not found")
		return
	}
	if _, getErr := cluster.client.AppsV1().Deployments(namespace).Get(r.Context(), appName, metav1.GetOptions{}); getErr == nil {
		writeError(w, http.StatusConflict, "A deployment with this name still exists; delete it before purging the retained volume")
		return
	} else if !apierrors.IsNotFound(getErr) {
		writeError(w, http.StatusBadGateway, "Could not verify whether the app still uses this volume")
		return
	}
	uid := claim.UID
	if err := claims.Delete(r.Context(), claimName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
		writeError(w, http.StatusBadGateway, "Could not delete retained app volume")
		return
	}
	if err := s.recordAudit(r.Context(), principal.workspaceID, principal.userID, "storage.delete", claimName); err != nil {
		log.Printf("audit retained app volume deletion: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *deploymentService) storagePrincipal(w http.ResponseWriter, r *http.Request, scope string, requireWrite bool) (requestPrincipal, *kubernetesProvisioner, string, bool) {
	principal, err := s.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Valid workspace authentication is required")
		return requestPrincipal{}, nil, "", false
	}
	if !principal.hasScope(scope) {
		writeError(w, http.StatusForbidden, "Token requires the "+scope+" scope")
		return requestPrincipal{}, nil, "", false
	}
	if principal.isSession && !s.auth.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "Request origin is not allowed")
		return requestPrincipal{}, nil, "", false
	}
	if requireWrite {
		var role string
		if err := s.auth.db.QueryRowContext(r.Context(), `SELECT role FROM workspace_members WHERE user_id = ? AND workspace_id = ?`, principal.userID, principal.workspaceID).Scan(&role); err != nil || (role != "owner" && role != "admin" && role != "developer") {
			writeError(w, http.StatusForbidden, "Workspace write access required")
			return requestPrincipal{}, nil, "", false
		}
	}
	provider, err := s.provisionerForContext(s.activeClusterContext())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Kubernetes storage management is not configured")
		return requestPrincipal{}, nil, "", false
	}
	cluster, ok := provider.(*kubernetesProvisioner)
	if !ok || cluster == nil {
		writeError(w, http.StatusServiceUnavailable, "Kubernetes storage management is unavailable for this provider")
		return requestPrincipal{}, nil, "", false
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), principal.workspaceID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return requestPrincipal{}, nil, "", false
	}
	return principal, cluster, namespace, true
}

func claimAccessMode(claim *corev1.PersistentVolumeClaim) string {
	for _, mode := range claim.Spec.AccessModes {
		if mode == corev1.ReadWriteMany {
			return "shared"
		}
	}
	return "single_writer"
}
