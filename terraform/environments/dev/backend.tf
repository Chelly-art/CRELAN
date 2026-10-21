terraform {
  backend "s3" {
    bucket         = "crelan-terraform-state-bucket-eu-west-2" # Replace with a globally unique bucket name if needed
    key            = "dev/eks/terraform.tfstate"
    region         = "eu-west-2"
    dynamodb_table = "crelan-terraform-locks"
    encrypt        = true
  }
}