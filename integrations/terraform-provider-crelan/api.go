package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

type deploymentResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Image         string `json:"image"`
	RepositoryURL string `json:"repository_url"`
	Branch        string `json:"branch"`
	Dockerfile    string `json:"dockerfile"`
	Port          int    `json:"port"`
	CPUMillicores int    `json:"cpu_millicores"`
	MemoryMB      int    `json:"memory_mb"`
	Status        string `json:"status"`
	URL           string `json:"url"`
}

type apiHTTPError struct {
	status  int
	message string
}

func (e *apiHTTPError) Error() string {
	return e.message
}

func (c *apiClient) request(ctx context.Context, method, path string, body any, result any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Crelan request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("create Crelan request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.http
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request Crelan API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Crelan response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &apiError)
		message := apiError.Error
		if message == "" {
			message = fmt.Sprintf("Crelan API returned HTTP %d", resp.StatusCode)
		} else if apiError.Code != "" {
			message += " (" + apiError.Code + ")"
		}
		return &apiHTTPError{status: resp.StatusCode, message: message}
	}
	if result == nil || resp.StatusCode == http.StatusNoContent || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode Crelan response: %w", err)
	}
	return nil
}

func (c *apiClient) wait(ctx context.Context, id int64, timeout time.Duration) (deploymentResponse, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var current deploymentResponse
		path := "/api/v1/deployments/" + strconv.FormatInt(id, 10)
		if err := c.request(waitCtx, http.MethodGet, path, nil, &current); err != nil {
			return deploymentResponse{}, err
		}
		switch current.Status {
		case "running":
			return current, nil
		case "failed", "deleted", "cancelled":
			return current, fmt.Errorf("deployment %d finished with status %s", id, current.Status)
		}
		select {
		case <-waitCtx.Done():
			return current, fmt.Errorf("timed out waiting for deployment %d (last status: %s)", id, current.Status)
		case <-ticker.C:
		}
	}
}
