package main

import (
	"context"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"api_url": {
				Type: schema.TypeString, Optional: true,
				DefaultFunc: schema.EnvDefaultFunc("CRELAN_API_URL", nil),
				Description: "Base URL of the Crelan instance. Defaults to CRELAN_API_URL.",
			},
			"token": {
				Type: schema.TypeString, Optional: true, Sensitive: true,
				DefaultFunc: schema.EnvDefaultFunc("CRELAN_TOKEN", nil),
				Description: "Workspace API token. Defaults to CRELAN_TOKEN.",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"crelan_deployment": resourceDeployment(),
		},
		ConfigureContextFunc: configureProvider,
	}
}

func configureProvider(_ context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	apiURL := strings.TrimRight(strings.TrimSpace(d.Get("api_url").(string)), "/")
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, diag.Errorf("api_url must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalHost(parsed.Hostname())) {
		return nil, diag.Errorf("api_url must use HTTPS (HTTP is allowed only for localhost)")
	}
	token := strings.TrimSpace(d.Get("token").(string))
	if token == "" {
		return nil, diag.Errorf("set the token provider argument or CRELAN_TOKEN")
	}
	return &apiClient{baseURL: apiURL, token: token}, nil
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
