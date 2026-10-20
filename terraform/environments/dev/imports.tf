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

# --- VPC & Networking ---
import {
  to = module.vpc.aws_vpc.main
  id = "vpc-052fe4f3adfd3f7de"
}

import {
  to = module.vpc.aws_internet_gateway.igw
  id = "igw-0aeb89a3dff424b20"
}

# --- Subnets ---
import {
  to = module.vpc.aws_subnet.public[0]
  id = "subnet-03553a7e8a5a9b385"
}

import {
  to = module.vpc.aws_subnet.public[1]
  id = "subnet-0d69fd58b4d67e52d"
}

import {
  to = module.vpc.aws_subnet.private[0]
  id = "subnet-0ce617b97ca715d8f"
}

import {
  to = module.vpc.aws_subnet.private[1]
  id = "subnet-0635e1f339c9dd71c"
}

# --- Route Tables ---
import {
  to = module.vpc.aws_route_table.public
  id = "rtb-0d966e17e3e4693fd"
}

# --- Route Table Associations ---
import {
  to = module.vpc.aws_route_table_association.public[0]
  id = "subnet-03553a7e8a5a9b385/rtb-0d966e17e3e4693fd"
}

import {
  to = module.vpc.aws_route_table_association.public[1]
  id = "subnet-0d69fd58b4d67e52d/rtb-0d966e17e3e4693fd"
}