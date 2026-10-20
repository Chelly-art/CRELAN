import {
  to = module.iam.aws_iam_role.eks_cluster_role
  id = "dev-eks-cluster-role"
}

import {
  to = module.iam.aws_iam_role.eks_node_role
  id = "dev-eks-node-role"
}

import {
  to = module.vpc.aws_vpc.main
  id = "vpc-052fe4f3adfd3f7de"
}