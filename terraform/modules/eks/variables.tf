variable "environment" {
  type        = string
  description = "Deployment environment name (e.g., dev, prod)"
}

variable "cluster_version" {
  type        = string
  description = "Kubernetes version for the EKS cluster"
  default     = "1.30"
}

variable "subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for the EKS control plane"
}

variable "node_subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs specifically for the EKS worker nodes (optional; defaults to general subnet_ids if left empty)"
  default     = []
}