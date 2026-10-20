package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthDoesNotExposeInfrastructureDetails(t *testing.T) {
	response := httptest.NewRecorder()
	healthHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("health response should expose only service status, got %v", body)
	}
}
