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

variable "node_ami_type" {
  type        = string
  description = "AMI type for the worker nodes (Amazon Linux 2023)"
  default     = "AL2023_x86_64_STANDARD"
}

variable "node_instance_types" {
  type        = list(string)
  description = "Instance types for the general node group"
  default     = ["m6i.xlarge"]
}

variable "node_disk_size" {
  type        = number
  description = "Root disk size in GiB for the general node group"
  default     = 50
}

variable "node_desired_size" {
  type        = number
  description = "Desired node count for the general node group"
  default     = 2
}

variable "node_min_size" {
  type        = number
  description = "Minimum node count for the general node group"
  default     = 2
}

variable "node_max_size" {
  type        = number
  description = "Maximum node count for the general node group"
  default     = 3
}

variable "kata_enabled" {
  type        = bool
  description = "Create the tainted Kata node group (needs bare metal or nested virtualization)"
  default     = false
}

variable "kata_instance_types" {
  type        = list(string)
  description = "Instance types for the Kata node group"
  default     = ["m5zn.metal"]
}

variable "kata_desired_size" {
  type        = number
  description = "Desired node count for the Kata node group"
  default     = 1
}

variable "kata_min_size" {
  type        = number
  description = "Minimum node count for the Kata node group"
  default     = 1
}

variable "kata_max_size" {
  type        = number
  description = "Maximum node count for the Kata node group"
  default     = 1
}