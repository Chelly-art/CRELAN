variable "app_name" {
  type        = string
  description = "Lowercase Crelan application name."
}

variable "image" {
  type        = string
  description = "Image reference already built and pushed by CI."
}

variable "image_pull_secret" {
  type        = string
  description = "Optional saved Crelan credential for a private registry."
  default     = null
}

resource "crelan_deployment" "app" {
  name              = var.app_name
  image             = var.image
  image_pull_secret = var.image_pull_secret
  port              = 8080
  wait_for_ready    = true
}

output "deployment_url" {
  value = crelan_deployment.app.url
}

output "deployment_status" {
  value = crelan_deployment.app.status
}
