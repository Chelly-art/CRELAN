package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "crelan:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("command is required")
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "crelan %s\n", version)
		return nil
	}

	apiURL := os.Getenv("CRELAN_API_URL")
	token := os.Getenv("CRELAN_TOKEN")
	commandArgs, configuredURL, err := extractAPIURL(args[1:], apiURL)
	if err != nil {
		return err
	}
	apiURL = configuredURL
	if token == "" {
		return errors.New("set CRELAN_TOKEN to a workspace access token")
	}
	base, err := parseBaseURL(apiURL)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	switch args[0] {
	case "deploy":
		return deploy(client, base, token, commandArgs, stdout)
	case "deployments", "list":
		if len(commandArgs) != 0 {
			return errors.New("usage: crelan deployments")
		}
		return listDeployments(client, base, token, stdout)
	case "delete":
		return deleteDeployment(client, base, token, commandArgs, stdout)
	case "status":
		return getDeployment(client, base, token, commandArgs, stdout)
	case "history":
		return deploymentSubresource(client, base, token, commandArgs, "history", stdout)
	case "logs":
		return deploymentSubresource(client, base, token, commandArgs, "logs", stdout)
	case "resources":
		return deploymentSubresource(client, base, token, commandArgs, "resources", stdout)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func extractAPIURL(args []string, defaultURL string) ([]string, string, error) {
	filtered := make([]string, 0, len(args))
	apiURL := defaultURL
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--api-url":
			if i+1 >= len(args) || args[i+1] == "" {
				return nil, "", errors.New("--api-url requires a value")
			}
			apiURL = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--api-url="):
			apiURL = strings.TrimPrefix(args[i], "--api-url=")
			if apiURL == "" {
				return nil, "", errors.New("--api-url requires a value")
			}
		default:
			filtered = append(filtered, args[i])
		}
	}
	return filtered, apiURL, nil
}

func deploy(client *http.Client, base, token string, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
	name := flags.String("name", "", "app name (required)")
	repository := flags.String("repo", "", "public GitHub repository URL (required)")
	branch := flags.String("branch", "", "branch to deploy")
	dockerfile := flags.String("dockerfile", "Dockerfile", "Dockerfile path in repository")
	port := flags.Int("port", 8080, "application HTTP port")
	cpu := flags.Int("cpu-millicores", 1000, "application CPU limit in millicores")
	memory := flags.Int("memory-mib", 512, "application memory limit in MiB")
	wait := flags.Bool("wait", false, "wait until deployment is running or fails")
	waitTimeout := flags.Duration("wait-timeout", 45*time.Minute, "maximum time to wait for deployment")
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *name == "" || *repository == "" {
		return errors.New("usage: crelan deploy --name APP --repo https://github.com/OWNER/REPO [--branch BRANCH] [--dockerfile PATH] [--port PORT]")
	}
	body, err := json.Marshal(map[string]any{
		"name": *name, "repository_url": *repository, "branch": *branch, "dockerfile": *dockerfile,
		"port": *port, "cpu_millicores": *cpu, "memory_mb": *memory,
	})
	if err != nil {
		return fmt.Errorf("encode deployment request: %w", err)
	}
	var response struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := doAPIRequest(client, http.MethodPost, base+"/api/v1/deployments", token, body, &response); err != nil {
		return err
	}
	if *wait {
		if *waitTimeout <= 0 {
			return errors.New("--wait-timeout must be positive")
		}
		return waitForDeployment(client, base, token, response.ID, *waitTimeout, stdout)
	}
	return printJSON(stdout, response)
}

func waitForDeployment(client *http.Client, base, token string, id int64, timeout time.Duration, stdout io.Writer) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var response map[string]any
		endpoint := base + "/api/v1/deployments/" + strconv.FormatInt(id, 10)
		if err := doAPIRequest(client, http.MethodGet, endpoint, token, nil, &response); err != nil {
			return err
		}
		status, _ := response["status"].(string)
		if status == "running" {
			return printJSON(stdout, response)
		}
		if status == "failed" || status == "deleted" || status == "cancelled" {
			_ = printJSON(stdout, response)
			return fmt.Errorf("deployment finished with status %s", status)
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for deployment %d; use `crelan status %d` to check progress", id, id)
		case <-ticker.C:
		}
	}
}

func listDeployments(client *http.Client, base, token string, stdout io.Writer) error {
	var response any
	if err := doAPIRequest(client, http.MethodGet, base+"/api/v1/deployments", token, nil, &response); err != nil {
		return err
	}
	return printJSON(stdout, response)
}

func getDeployment(client *http.Client, base, token string, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: crelan status DEPLOYMENT_ID")
	}
	id, err := parseDeploymentID(args[0])
	if err != nil {
		return err
	}
	var response any
	if err := doAPIRequest(client, http.MethodGet, base+"/api/v1/deployments/"+strconv.FormatInt(id, 10), token, nil, &response); err != nil {
		return err
	}
	return printJSON(stdout, response)
}

func deploymentSubresource(client *http.Client, base, token string, args []string, resource string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: crelan %s DEPLOYMENT_ID", resource)
	}
	id, err := parseDeploymentID(args[0])
	if err != nil {
		return err
	}
	endpoint := base + "/api/v1/deployments/" + strconv.FormatInt(id, 10) + "/" + resource
	if resource == "logs" {
		endpoint += "?tail=200"
	}
	var response any
	if err := doAPIRequest(client, http.MethodGet, endpoint, token, nil, &response); err != nil {
		return err
	}
	return printJSON(stdout, response)
}

func parseDeploymentID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("deployment ID must be a positive integer")
	}
	return id, nil
}

func deleteDeployment(client *http.Client, base, token string, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: crelan delete DEPLOYMENT_ID")
	}
	id, err := parseDeploymentID(args[0])
	if err != nil {
		return err
	}
	var response any
	err = doAPIRequest(client, http.MethodDelete, base+"/api/v1/deployments/"+url.PathEscape(strconv.FormatInt(id, 10)), token, nil, &response)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Deployment deleted")
	return nil
}

func doAPIRequest(client *http.Client, method, endpoint, token string, body []byte, result any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request Crelan API: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Crelan API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(responseBody, &apiError) == nil && apiError.Error != "" {
			if apiError.Code != "" {
				return fmt.Errorf("%s (%s)", apiError.Error, apiError.Code)
			}
			return errors.New(apiError.Error)
		}
		return fmt.Errorf("Crelan API returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusNoContent || len(responseBody) == 0 || result == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode Crelan API response: %w", err)
	}
	return nil
}

func parseBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("set CRELAN_API_URL or pass --api-url")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Crelan API URL must be an absolute HTTP(S) URL without credentials or query")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalHost(parsed.Hostname())) {
		return "", errors.New("Crelan API URL must use HTTPS (HTTP is allowed only for localhost)")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func printJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("print result: %w", err)
	}
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  crelan deploy --name APP --repo https://github.com/OWNER/REPO [--branch BRANCH] [--dockerfile PATH] [--port PORT]
  crelan deployments
  crelan status DEPLOYMENT_ID
  crelan history DEPLOYMENT_ID
  crelan logs DEPLOYMENT_ID
  crelan resources DEPLOYMENT_ID
  crelan delete DEPLOYMENT_ID
  crelan version

Set CRELAN_API_URL and CRELAN_TOKEN for API access.`)
}
