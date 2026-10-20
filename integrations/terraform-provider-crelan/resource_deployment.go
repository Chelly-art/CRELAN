package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceDeployment() *schema.Resource {
	return &schema.Resource{
		CreateContext: createDeployment,
		ReadContext:   readDeployment,
		UpdateContext: updateDeployment,
		DeleteContext: deleteDeployment,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"name":                 {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Lowercase workspace application name."},
			"image":                {Type: schema.TypeString, Optional: true, ExactlyOneOf: []string{"image", "repository_url"}, ConflictsWith: []string{"repository_url", "branch"}},
			"image_pull_secret":    {Type: schema.TypeString, Optional: true, ConflictsWith: []string{"repository_url"}, Description: "Name of a saved workspace registry credential."},
			"repository_url":       {Type: schema.TypeString, Optional: true, ExactlyOneOf: []string{"image", "repository_url"}, ConflictsWith: []string{"image_pull_secret"}, Description: "HTTPS GitHub repository URL. Private repositories require a connected GitHub App."},
			"branch":               {Type: schema.TypeString, Optional: true, ConflictsWith: []string{"image"}},
			"dockerfile":           {Type: schema.TypeString, Optional: true, Default: "Dockerfile"},
			"port":                 {Type: schema.TypeInt, Optional: true, Default: 8080},
			"cpu_millicores":       {Type: schema.TypeInt, Optional: true, Default: 1000},
			"memory_mb":            {Type: schema.TypeInt, Optional: true, Default: 512},
			"wait_for_ready":       {Type: schema.TypeBool, Optional: true, Default: true, Description: "Wait for the deployment to reach running status before apply completes."},
			"wait_timeout_seconds": {Type: schema.TypeInt, Optional: true, Default: 2700},
			"deployment_id":        {Type: schema.TypeInt, Computed: true},
			"status":               {Type: schema.TypeString, Computed: true},
			"url":                  {Type: schema.TypeString, Computed: true},
		},
	}
}

func createDeployment(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return applyDeployment(ctx, d, meta.(*apiClient))
}

func updateDeployment(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return applyDeployment(ctx, d, meta.(*apiClient))
}

func applyDeployment(ctx context.Context, d *schema.ResourceData, client *apiClient) diag.Diagnostics {
	request := map[string]any{
		"name": d.Get("name").(string), "port": d.Get("port").(int),
		"cpu_millicores": d.Get("cpu_millicores").(int), "memory_mb": d.Get("memory_mb").(int),
	}
	if image := d.Get("image").(string); image != "" {
		request["image"] = image
		if secret := d.Get("image_pull_secret").(string); secret != "" {
			request["image_pull_secret"] = secret
		}
	} else {
		request["repository_url"] = d.Get("repository_url").(string)
		request["branch"] = d.Get("branch").(string)
		request["dockerfile"] = d.Get("dockerfile").(string)
	}
	var created deploymentResponse
	if err := client.request(ctx, http.MethodPost, "/api/v1/deployments", request, &created); err != nil {
		return diag.Errorf("create Crelan deployment: %v", err)
	}
	d.SetId(strconv.FormatInt(created.ID, 10))
	if d.Get("wait_for_ready").(bool) {
		timeout := time.Duration(d.Get("wait_timeout_seconds").(int)) * time.Second
		ready, err := client.wait(ctx, created.ID, timeout)
		if err != nil {
			return diag.Errorf("wait for Crelan deployment: %v", err)
		}
		created = ready
	}
	return setDeploymentState(d, created)
}

func readDeployment(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)
	id, err := strconv.ParseInt(d.Id(), 10, 64)
	if err != nil || id < 1 {
		return diag.Errorf("invalid Crelan deployment ID in state")
	}
	var current deploymentResponse
	path := "/api/v1/deployments/" + strconv.FormatInt(id, 10)
	if err := client.request(ctx, http.MethodGet, path, nil, &current); err != nil {
		var apiErr *apiHTTPError
		if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
			d.SetId("")
			return nil
		}
		return diag.Errorf("read Crelan deployment: %v", err)
	}
	return setDeploymentState(d, current)
}

func deleteDeployment(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*apiClient)
	path := "/api/v1/deployments/" + d.Id()
	if err := client.request(ctx, http.MethodDelete, path, nil, nil); err != nil {
		var apiErr *apiHTTPError
		if !errors.As(err, &apiErr) || apiErr.status != http.StatusNotFound {
			return diag.Errorf("delete Crelan deployment: %v", err)
		}
	}
	d.SetId("")
	return nil
}

func setDeploymentState(d *schema.ResourceData, state deploymentResponse) diag.Diagnostics {
	if state.ID > 0 {
		d.SetId(strconv.FormatInt(state.ID, 10))
	}
	for key, value := range map[string]any{
		"name":           state.Name,
		"port":           state.Port,
		"cpu_millicores": state.CPUMillicores,
		"memory_mb":      state.MemoryMB,
		"deployment_id":  state.ID,
		"status":         state.Status,
		"url":            state.URL,
	} {
		if err := d.Set(key, value); err != nil {
			return diag.Errorf("save Crelan deployment %s: %v", key, err)
		}
	}
	if state.Image != "" {
		if err := d.Set("image", state.Image); err != nil {
			return diag.Errorf("save Crelan deployment image: %v", err)
		}
		if err := d.Set("repository_url", ""); err != nil {
			return diag.Errorf("save Crelan deployment source: %v", err)
		}
		if err := d.Set("branch", ""); err != nil {
			return diag.Errorf("save Crelan deployment branch: %v", err)
		}
	} else if state.RepositoryURL != "" {
		if err := d.Set("image", ""); err != nil {
			return diag.Errorf("save Crelan deployment image: %v", err)
		}
		if err := d.Set("repository_url", state.RepositoryURL); err != nil {
			return diag.Errorf("save Crelan deployment source: %v", err)
		}
		if err := d.Set("branch", state.Branch); err != nil {
			return diag.Errorf("save Crelan deployment branch: %v", err)
		}
		if err := d.Set("dockerfile", state.Dockerfile); err != nil {
			return diag.Errorf("save Crelan deployment Dockerfile: %v", err)
		}
	}
	if state.ID < 1 {
		return diag.Errorf("Crelan returned an invalid deployment ID")
	}
	if state.Name != "" && state.Name != d.Get("name").(string) {
		return diag.Errorf("Crelan returned unexpected application %q", state.Name)
	}
	return nil
}
