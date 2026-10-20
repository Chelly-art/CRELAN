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

# --- EKS Cluster ---
import {
  to = module.eks.aws_eks_cluster.main
  id = "dev-cluster"
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

# --- Route Tables / Associations ---
import {
  to = module.vpc.aws_route_table.public
  id = "rtb-0d966e17e3e4693fd"
}