
module "vpc" {
  source = "../../modules/vpc"

  environment        = var.environment
  vpc_cidr           = var.vpc_cidr
  availability_zones = ["eu-west-2a", "eu-west-2b"]
}

module "iam" {
  source = "../../modules/iam"

  environment                  = var.environment
  cluster_name                 = "dev-cluster" # Pass a static string or name here to avoid circular dependencies with EKS
  cluster_identity_oidc_issuer = module.eks.cluster_oidc_issuer
}

module "eks" {
  source = "../../modules/eks"

  environment     = var.environment
  cluster_name    = "dev-cluster"
  cluster_version = "1.36"

  # Control plane stays secure in private subnets
  subnet_ids = module.vpc.private_subnets

  # Worker nodes get public IPs and internet access to join the cluster
  node_subnet_ids = module.vpc.public_subnets

  # Connect the IAM roles created by the iam module
  cluster_role_arn = module.iam.cluster_role_arn
  node_role_arn    = module.iam.node_role_arn
}