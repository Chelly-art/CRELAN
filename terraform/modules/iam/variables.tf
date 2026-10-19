variable "environment" {
  type        = string
  description = "Environment name"
}

variable "cluster_name" {
  type        = string
  description = "Name of the EKS cluster"
}

variable "cluster_identity_oidc_issuer" {
  type        = string
  description = "The OIDC Issuer URL for the EKS cluster"
}
