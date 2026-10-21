locals {
  node_subnets = length(var.node_subnet_ids) > 0 ? var.node_subnet_ids : var.subnet_ids
}

resource "aws_eks_cluster" "main" {
  name     = var.cluster_name
  role_arn = var.cluster_role_arn
  version  = var.cluster_version

  vpc_config {
    subnet_ids = var.subnet_ids
  }
}

# Essential Core Add-ons for networking and DNS resolution
resource "aws_eks_addon" "vpc_cni" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "vpc-cni"
  resolve_conflicts_on_update = "OVERWRITE"
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "kube-proxy"
  resolve_conflicts_on_update = "OVERWRITE"
}

resource "aws_eks_addon" "coredns" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "coredns"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [aws_eks_addon.vpc_cni]
}

# General node group. Same resource address and name as before (aws_eks_node_group.nodes / dev-node-group),
# so Terraform replaces it in place instead of creating a second group.
resource "aws_eks_node_group" "nodes" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "${var.environment}-node-group"
  node_role_arn   = aws_iam_role.eks_node_role.arn
  subnet_ids      = local.node_subnets

  ami_type       = var.node_ami_type # Amazon Linux 2023
  instance_types = var.node_instance_types
  capacity_type  = "ON_DEMAND"
  disk_size      = var.node_disk_size

  scaling_config {
    desired_size = var.node_desired_size
    min_size     = var.node_min_size
    max_size     = var.node_max_size
  }

  labels = {
    workload = "general"
  }

  timeouts {
    create = "15m"
    delete = "15m"
  }

  depends_on = [
    aws_eks_addon.vpc_cni,
    aws_eks_addon.kube_proxy,
    aws_iam_role_policy_attachment.eks_worker_node_policy,
    aws_iam_role_policy_attachment.eks_cni_policy,
    aws_iam_role_policy_attachment.eks_ecr_read_only,
  ]
}

# Kata node group: OFF by default. Needs bare metal (or nested virtualization) and a larger vCPU quota.
resource "aws_eks_node_group" "kata" {
  count = var.kata_enabled ? 1 : 0

  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "${var.environment}-kata-group"
  node_role_arn   = aws_iam_role.eks_node_role.arn
  subnet_ids      = [local.node_subnets[0]]

  ami_type       = var.node_ami_type
  instance_types = var.kata_instance_types
  capacity_type  = "ON_DEMAND"
  disk_size      = 100

  scaling_config {
    desired_size = var.kata_desired_size
    min_size     = var.kata_min_size
    max_size     = var.kata_max_size
  }

  labels = {
    workload = "kata"
  }

  taint {
    key    = "kata"
    value  = "true"
    effect = "NO_SCHEDULE"
  }

  timeouts {
    create = "20m"
    delete = "20m"
  }

  depends_on = [
    aws_eks_node_group.nodes,
    aws_iam_role_policy_attachment.eks_worker_node_policy,
    aws_iam_role_policy_attachment.eks_cni_policy,
    aws_iam_role_policy_attachment.eks_ecr_read_only,
  ]
}