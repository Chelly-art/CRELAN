# Private image registry access

Crelan must authenticate to a private image registry independently of the
Kubernetes cluster's AWS account. A customer can use a registry hosted in a
different cloud account from Crelan.

## Customer flow

1. The customer chooses **Connect registry** in their workspace.
2. Crelan presents provider-specific authorization. For AWS ECR, the customer
   launches `web/aws-ecr-read-role.yaml` in their AWS account. The stack creates
   one IAM role with read-only access to one ECR repository and trusts the
   Crelan AWS account using a customer-specific external ID.
3. The customer returns the role ARN from the CloudFormation stack output.
4. Crelan assumes that role, obtains short-lived ECR registry credentials, and
   refreshes the workspace's Kubernetes pull credential before the ECR token
   expires. Crelan must never ask for AWS access keys.
5. The customer deploys by selecting or entering an image URI.

CloudFormation is only a guided way to create the customer's IAM role. It does
not create compute, networking, or a Kubernetes cluster in their account.

## Other registries

- **Azure Container Registry**: use Microsoft Entra workload identity
  federation and a registry-scoped pull role.
- **Google Artifact Registry**: use Workload Identity Federation and grant
  repository-scoped Artifact Registry Reader access.
- **Docker Hub and GHCR**: accept a read-only registry token and store it as an
  opaque credential; never return its value after save.
- **Public images**: no connection is needed.

The UI should show one provider-neutral registry connection list. Each
provider adapter exchanges its authorization for temporary pull credentials
and refreshes them without exposing credentials to application containers.

## Runtime requirements before enabling AWS ECR

The CloudFormation template is a customer-side permission template, not a
complete connector by itself. The Crelan server still needs an AWS workload
identity in its own account, a server-side AssumeRole/ECR adapter, and a
refresh worker that updates the per-workspace Kubernetes pull Secret. Keep
these as separate deployment components so the server does not rely on the
Kubernetes node role having customer-account access.
