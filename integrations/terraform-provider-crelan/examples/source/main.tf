terraform {
  required_providers {
    crelan = {
      source  = "crelan/crelan"
      version = "0.1.0"
    }
  }
}

provider "crelan" {}

resource "crelan_deployment" "app" {
  name           = "storefront"
  repository_url = "https://github.com/acme/storefront"
  branch         = "main"
  dockerfile     = "Dockerfile"
  port           = 8080
  wait_for_ready = true
}

output "deployment_url" {
  value = crelan_deployment.app.url
}
