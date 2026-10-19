output "oidc_provider_arn" {
  description = "ARN of the OIDC Provider for EKS IRSA"
  value       = aws_iam_openid_connect_provider.eks.arn
}

output "oidc_provider_url" {
  description = "URL of the OIDC Provider"
  value       = aws_iam_openid_connect_provider.eks.url
}
