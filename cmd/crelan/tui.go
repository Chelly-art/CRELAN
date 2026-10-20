package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func runTUI(client *http.Client, base, token string, input io.Reader, output io.Writer) error {
	reader := bufio.NewScanner(input)
	for {
		fmt.Fprintln(output, "\nCrelan")
		fmt.Fprintln(output, "  1  List deployments")
		fmt.Fprintln(output, "  2  Deploy an image")
		fmt.Fprintln(output, "  3  Build and deploy a GitHub repository")
		fmt.Fprintln(output, "  4  View deployment status")
		fmt.Fprintln(output, "  5  View deployment logs")
		fmt.Fprintln(output, "  6  View deployment resources")
		fmt.Fprintln(output, "  7  Delete a deployment")
		fmt.Fprintln(output, "  q  Quit")
		choice, err := tuiReadLine(reader, output, "Select an option: ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if choice == "q" || choice == "Q" {
			return nil
		}
		var actionErr error
		switch choice {
		case "1":
			actionErr = tuiListDeployments(client, base, token, output)
		case "2":
			actionErr = tuiDeployImage(client, base, token, reader, output)
		case "3":
			actionErr = tuiDeployRepository(client, base, token, reader, output)
		case "4":
			actionErr = tuiDeploymentSubresource(client, base, token, reader, output, "status")
		case "5":
			actionErr = tuiDeploymentSubresource(client, base, token, reader, output, "logs")
		case "6":
			actionErr = tuiDeploymentSubresource(client, base, token, reader, output, "resources")
		case "7":
			actionErr = tuiDeleteDeployment(client, base, token, reader, output)
		default:
			fmt.Fprintln(output, "Choose one of the listed options.")
		}
		if actionErr != nil {
			if errors.Is(actionErr, io.EOF) {
				return nil
			}
			fmt.Fprintf(output, "\nError: %v\n", actionErr)
		}
		if choice >= "1" && choice <= "7" {
			if _, err := tuiReadLine(reader, output, "\nPress Enter to return to the menu..."); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}
}

func tuiListDeployments(client *http.Client, base, token string, output io.Writer) error {
	var items []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := doAPIRequest(client, http.MethodGet, base+"/api/v1/deployments", token, nil, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(output, "No deployments yet.")
		return nil
	}
	fmt.Fprintf(output, "\n%-6s %-28s %-14s %s\n", "ID", "NAME", "STATUS", "URL")
	fmt.Fprintf(output, "%-6s %-28s %-14s %s\n", "------", "----------------------------", "--------------", "---")
	for _, item := range items {
		fmt.Fprintf(output, "%-6d %-28s %-14s %s\n", item.ID, truncateTUI(item.Name, 28), item.Status, item.URL)
	}
	return nil
}

func tuiDeployImage(client *http.Client, base, token string, reader *bufio.Scanner, output io.Writer) error {
	name, err := tuiRequired(reader, output, "App name: ")
	if err != nil {
		return err
	}
	image, err := tuiRequired(reader, output, "Image reference: ")
	if err != nil {
		return err
	}
	secret, err := tuiReadLine(reader, output, "Saved image pull credential (optional): ")
	if err != nil {
		return err
	}
	port, err := tuiDefault(reader, output, "Application port", "8080")
	if err != nil {
		return err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return errors.New("port must be a number")
	}
	args := []string{"--name", name, "--image", image, "--port", port, "--wait"}
	if secret != "" {
		args = append(args, "--image-pull-secret", secret)
	}
	return deploy(client, base, token, args, output)
}

func tuiDeployRepository(client *http.Client, base, token string, reader *bufio.Scanner, output io.Writer) error {
	name, err := tuiRequired(reader, output, "App name: ")
	if err != nil {
		return err
	}
	repository, err := tuiRequired(reader, output, "GitHub repository URL: ")
	if err != nil {
		return err
	}
	branch, err := tuiReadLine(reader, output, "Branch (optional): ")
	if err != nil {
		return err
	}
	dockerfile, err := tuiDefault(reader, output, "Dockerfile path", "Dockerfile")
	if err != nil {
		return err
	}
	port, err := tuiDefault(reader, output, "Application port", "8080")
	if err != nil {
		return err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return errors.New("port must be a number")
	}
	args := []string{"--name", name, "--repo", repository, "--dockerfile", dockerfile, "--port", port, "--wait"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	return deploy(client, base, token, args, output)
}

func tuiDeploymentSubresource(client *http.Client, base, token string, reader *bufio.Scanner, output io.Writer, resource string) error {
	id, err := tuiRequired(reader, output, "Deployment ID: ")
	if err != nil {
		return err
	}
	if resource == "status" {
		return getDeployment(client, base, token, []string{id}, output)
	}
	return deploymentSubresource(client, base, token, []string{id}, resource, output)
}

func tuiDeleteDeployment(client *http.Client, base, token string, reader *bufio.Scanner, output io.Writer) error {
	id, err := tuiRequired(reader, output, "Deployment ID to delete: ")
	if err != nil {
		return err
	}
	confirm, err := tuiReadLine(reader, output, "Type 'delete' to confirm: ")
	if err != nil {
		return err
	}
	if confirm != "delete" {
		fmt.Fprintln(output, "Deletion cancelled.")
		return nil
	}
	return deleteDeployment(client, base, token, []string{id}, output)
}

func tuiRequired(reader *bufio.Scanner, output io.Writer, prompt string) (string, error) {
	for {
		value, err := tuiReadLine(reader, output, prompt)
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
		fmt.Fprintln(output, "A value is required.")
	}
}

func tuiDefault(reader *bufio.Scanner, output io.Writer, prompt, fallback string) (string, error) {
	value, err := tuiReadLine(reader, output, prompt+" ["+fallback+"]: ")
	if err != nil || value != "" {
		return value, err
	}
	return fallback, nil
}

func tuiReadLine(reader *bufio.Scanner, output io.Writer, prompt string) (string, error) {
	fmt.Fprint(output, prompt)
	if !reader.Scan() {
		if err := reader.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(reader.Text()), nil
}

func truncateTUI(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max-1] + "…"
}
