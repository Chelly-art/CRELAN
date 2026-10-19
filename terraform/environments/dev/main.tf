module "vpc" {
  source = "../../modules/vpc"

  environment        = var.environment
  vpc_cidr           = var.vpc_cidr
  availability_zones = ["eu-west-2a", "eu-west-2b"]
}

module "eks" {
  source = "../../modules/eks"

  environment     = var.environment
  vpc_id          = module.vpc.vpc_id
  
  # Control plane stays secure in private subnets
  subnet_ids      = module.vpc.private_subnets
  
  # Worker nodes get public IPs and internet access to join the cluster
  node_subnet_ids = module.vpc.public_subnets
  
  cluster_version = "1.30"
}

module "iam" {
  source = "../../modules/iam"

  environment                  = var.environment
  cluster_name                 = module.eks.cluster_name
  cluster_identity_oidc_issuer = module.eks.cluster_oidc_issuer
}