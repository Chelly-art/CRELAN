package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderAddr: "registry.terraform.io/crelan/crelan",
		ProviderFunc: provider,
	})
}
