# --- IAM Roles ---
import {
  to = module.iam.aws_iam_role.eks_cluster_role
  id = "dev-eks-cluster-role"
}

import {
  to = module.iam.aws_iam_role.eks_node_role
  id = "dev-eks-node-role"
}

import {
  to = module.eks.aws_iam_role.eks_node_role
  id = "dev-cluster-node-group-role"
}

# --- IAM OIDC Provider ---
import {
  to = module.iam.aws_iam_openid_connect_provider.eks
  id = "arn:aws:iam::049637491884:oidc-provider/oidc.eks.eu-west-2.amazonaws.com/id/EFA40968B933AA786E7DEB5C0B897F97"
}

# --- EKS Cluster ---
import {
  to = module.eks.aws_eks_cluster.main
  id = "dev-cluster"
}

# --- EKS Add-ons ---
import {
  to = module.eks.aws_eks_addon.vpc_cni
  id = "dev-cluster:vpc-cni"
}

import {
  to = module.eks.aws_eks_addon.kube_proxy
  id = "dev-cluster:kube-proxy"
}

import {
  to = module.eks.aws_eks_addon.coredns
  id = "dev-cluster:coredns"
}

# --- EKS Node Groups ---
import {
  to = module.eks.aws_eks_node_group.dev_nodes
  id = "dev-cluster:dev-cluster-node-group"
}

# --- VPC Core Only ---
import {
  to = module.vpc.aws_vpc.main
  id = "vpc-052fe4f3adfd3f7de"
}

import {
  to = module.vpc.aws_internet_gateway.igw
  id = "igw-0aeb89a3dff424b20"
}