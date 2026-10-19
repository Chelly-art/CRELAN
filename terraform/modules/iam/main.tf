# Get TLS Certificate for the EKS OIDC Issuer
data "tls_certificate" "eks" {
  url = var.cluster_identity_oidc_issuer
}

# Create IAM OpenID Connect Provider for EKS
resource "aws_iam_openid_connect_provider" "eks" {
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks.certificates[0].sha1_fingerprint]
  url             = var.cluster_identity_oidc_issuer

  tags = {
    Name        = "${var.environment}-eks-oidc-provider"
    Environment = var.environment
  }
}
