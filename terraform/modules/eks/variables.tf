variable "environment" {
  type        = string
  description = "Environment name"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID where EKS will be deployed"
}

variable "subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for EKS worker nodes"
}

variable "cluster_version" {
  type        = string
  description = "Kubernetes version"
  default     = "1.30"
}
