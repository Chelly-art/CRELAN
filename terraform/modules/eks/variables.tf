variable "environment" {
  type        = string
  description = "Deployment environment name (e.g., dev, prod)"
}

variable "cluster_version" {
  type        = string
  description = "Kubernetes version for the EKS cluster"
  default     = "1.36"
}

variable "subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for the EKS control plane"
}

variable "node_subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for the EKS worker nodes (falls back to subnet_ids if empty)"
  default     = []
}

variable "cluster_name" {
  type        = string
  description = "Name of the EKS cluster"
}

variable "cluster_role_arn" {
  type        = string
  description = "IAM role ARN for the EKS cluster control plane"
}

variable "node_role_arn" {
  type        = string
  description = "IAM role ARN for the EKS worker node groups"
}