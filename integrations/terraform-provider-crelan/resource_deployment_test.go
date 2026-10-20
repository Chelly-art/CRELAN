package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestDeploymentCreateReadUpdateDelete(t *testing.T) {
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/deployments":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode deployment request: %v", err)
			}
			if body["name"] != "storefront" || body["port"] != float64(8080) {
				t.Errorf("unexpected deployment request: %#v", body)
			}
			postCount++
			wantImage := "registry.example/storefront:v1"
			if postCount > 1 {
				wantImage = "registry.example/storefront:v2"
			}
			if body["image"] != wantImage {
				t.Errorf("image = %v, want %q", body["image"], wantImage)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(deploymentResponse{
				ID: 41, Name: "storefront", Image: wantImage, Port: 8080,
				CPUMillicores: 500, MemoryMB: 512, Status: "queued",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/deployments/41":
			_ = json.NewEncoder(w).Encode(deploymentResponse{
				ID: 41, Name: "storefront", Image: "registry.example/storefront:v1",
				Port: 8080, CPUMillicores: 500, MemoryMB: 512,
				Status: "running", URL: "https://storefront.example",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/deployments/41":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &apiClient{baseURL: server.URL, token: "test-token", http: server.Client()}
	d := schema.TestResourceDataRaw(t, resourceDeployment().Schema, map[string]interface{}{
		"name": "storefront", "image": "registry.example/storefront:v1",
		"port": 8080, "cpu_millicores": 500, "memory_mb": 512,
		"wait_for_ready": false,
	})

	if diags := createDeployment(context.Background(), d, client); diags.HasError() {
		t.Fatalf("create deployment: %v", diags)
	}
	if d.Id() != "41" || d.Get("status") != "queued" {
		t.Fatalf("created state: id=%q status=%q", d.Id(), d.Get("status"))
	}

	if diags := readDeployment(context.Background(), d, client); diags.HasError() {
		t.Fatalf("read deployment: %v", diags)
	}
	if d.Get("status") != "running" || d.Get("url") != "https://storefront.example" {
		t.Fatalf("read state: status=%q url=%q", d.Get("status"), d.Get("url"))
	}

	if err := d.Set("image", "registry.example/storefront:v2"); err != nil {
		t.Fatal(err)
	}
	if diags := updateDeployment(context.Background(), d, client); diags.HasError() {
		t.Fatalf("update deployment: %v", diags)
	}
	if postCount != 2 {
		t.Fatalf("POST count = %d, want 2", postCount)
	}

	if diags := deleteDeployment(context.Background(), d, client); diags.HasError() {
		t.Fatalf("delete deployment: %v", diags)
	}
	if d.Id() != "" {
		t.Fatalf("delete left resource ID %q", d.Id())
	}
}

func TestDeploymentCreateSupportsGitHubSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/deployments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode deployment request: %v", err)
		}
		if body["repository_url"] != "https://github.com/acme/storefront" || body["branch"] != "main" {
			t.Errorf("unexpected source request: %#v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintln(w, `{"id":42,"name":"storefront","repository_url":"https://github.com/acme/storefront","branch":"main","dockerfile":"Dockerfile","port":8080,"cpu_millicores":1000,"memory_mb":512,"status":"queued"}`)
	}))
	defer server.Close()

	client := &apiClient{baseURL: server.URL, token: "test-token", http: server.Client()}
	d := schema.TestResourceDataRaw(t, resourceDeployment().Schema, map[string]interface{}{
		"name": "storefront", "repository_url": "https://github.com/acme/storefront",
		"branch": "main", "port": 8080, "wait_for_ready": false,
	})
	if diags := createDeployment(context.Background(), d, client); diags.HasError() {
		t.Fatalf("create source deployment: %v", diags)
	}
	if d.Id() != "42" || d.Get("repository_url") != "https://github.com/acme/storefront" {
		t.Fatalf("created source state: id=%q repository_url=%q", d.Id(), d.Get("repository_url"))
	}
}
