package main

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type deploymentWarningEvent struct {
	Type      string    `json:"type"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Object    string    `json:"object"`
	Count     int32     `json:"count"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

func (s *deploymentService) deploymentEvents(w http.ResponseWriter, r *http.Request) {
	principal, item, ok := s.authorizedDeployment(w, r, "deployments:read")
	if !ok {
		return
	}
	provider, err := s.provisionerForContext(item.ClusterContext)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Application event history is unavailable for this cluster")
		return
	}
	cluster, ok := provider.(*kubernetesProvisioner)
	if !ok || cluster == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []deploymentWarningEvent{}})
		return
	}
	namespace, err := cluster.workspaceNamespace(r.Context(), principal.workspaceID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	pods, err := cluster.client.CoreV1().Pods(namespace).List(r.Context(), metav1.ListOptions{LabelSelector: workspaceLabel + "=" + strconv.FormatInt(principal.workspaceID, 10) + "," + applicationLabel + "=" + item.Name})
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not load application warning events")
		return
	}
	podNames := make(map[string]struct{}, len(pods.Items))
	for i := range pods.Items {
		if ownsWorkspace(pods.Items[i].Labels, principal.workspaceID) && pods.Items[i].Labels[applicationLabel] == item.Name {
			podNames[pods.Items[i].Name] = struct{}{}
		}
	}
	ownedObjects := map[string]struct{}{
		item.Name: {}, appDataPVCName(item.Name): {}, applicationResourceName(item.Name, "env"): {},
		applicationResourceName(item.Name, "secrets"): {},
	}
	events, err := cluster.client.CoreV1().Events(namespace).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not load application warning events")
		return
	}
	items := make([]deploymentWarningEvent, 0, 10)
	for i := range events.Items {
		event := &events.Items[i]
		if event.Type != corev1.EventTypeWarning {
			continue
		}
		if _, ok := podNames[event.InvolvedObject.Name]; !ok {
			if _, ok := ownedObjects[event.InvolvedObject.Name]; !ok {
				continue
			}
		}
		last := event.LastTimestamp.Time
		if !event.EventTime.IsZero() && event.EventTime.Time.After(last) {
			last = event.EventTime.Time
		}
		if last.IsZero() {
			last = event.CreationTimestamp.Time
		}
		first := event.FirstTimestamp.Time
		if first.IsZero() {
			first = event.CreationTimestamp.Time
		}
		items = append(items, deploymentWarningEvent{
			Type: event.Type, Reason: event.Reason, Message: event.Message, Object: event.InvolvedObject.Name,
			Count: event.Count, FirstSeen: first.UTC(), LastSeen: last.UTC(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastSeen.After(items[j].LastSeen) })
	if len(items) > 30 {
		items = items[:30]
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}
