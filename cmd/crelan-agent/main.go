package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type command struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Port  int    `json:"port"`
}
type result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Logs    string `json:"logs,omitempty"`
}

func main() {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CRELAN_AGENT_URL")), "/")
	token := strings.TrimSpace(os.Getenv("CRELAN_AGENT_TOKEN"))
	if baseURL == "" || token == "" {
		fmt.Fprintln(os.Stderr, "CRELAN_AGENT_URL and CRELAN_AGENT_TOKEN are required")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for {
		ctx := context.Background()
		if err := request(ctx, client, baseURL, token, "POST", "/api/v1/agent/heartbeat", nil, nil); err != nil {
			time.Sleep(10 * time.Second)
			continue
		}
		var task command
		err := request(ctx, client, baseURL, token, "GET", "/api/v1/agent/commands", nil, &task)
		if errors.Is(err, errNoContent) {
			time.Sleep(5 * time.Second)
			continue
		}
		if err != nil {
			time.Sleep(10 * time.Second)
			continue
		}
		out := runContainer(ctx, task)
		_ = request(ctx, client, baseURL, token, "POST", fmt.Sprintf("/api/v1/agent/commands/%d/result", task.ID), out, nil)
	}
}

var errNoContent = errors.New("no command")

func request(ctx context.Context, client *http.Client, baseURL, token, method, path string, body any, out any) error {
	var raw io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, raw)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		if out != nil {
			return errNoContent
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Crelan returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out)
}

func runContainer(ctx context.Context, task command) result {
	containerName := "crelan-" + fmt.Sprint(task.ID)
	if _, err := exec.CommandContext(ctx, "docker", "pull", task.Image).CombinedOutput(); err != nil {
		return result{Status: "failed", Message: "Could not pull container image: " + err.Error()}
	}
	_, _ = exec.CommandContext(ctx, "docker", "rm", "-f", containerName).CombinedOutput()
	port := fmt.Sprintf("%d:%d", task.Port, task.Port)
	if output, err := exec.CommandContext(ctx, "docker", "run", "-d", "--restart", "unless-stopped", "--name", containerName, "-p", port, task.Image).CombinedOutput(); err != nil {
		return result{Status: "failed", Message: "Could not start container: " + strings.TrimSpace(string(output))}
	}
	logs, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", "100", containerName).CombinedOutput()
	return result{Status: "running", Message: "Container started", Logs: string(logs)}
}
