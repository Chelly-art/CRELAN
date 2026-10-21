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

## Active Kubernetes registry credentials workflow

In Crelan, private container registry credentials are saved directly to your workspace namespace as native Kubernetes `kubernetes.io/dockerconfigjson` Secrets (`imagePullSecrets`):

1. **Add credential:**
   - In the dashboard, click **Container image** when creating a deployment, then click **＋ Add registry credential**.
   - Or call `POST /api/v1/registry-credentials`:
     ```json
     {
       "name": "ghcr-production",
       "server": "ghcr.io",
       "username": "deploy-robot",
       "password": "<token>"
     }
     ```
2. **Kubernetes Secret creation:** Crelan serializes the authentication entry into `.dockerconfigjson` format and persists it as a Secret named `ghcr-production` in your tenant namespace (`crelan-ws-<workspace-id>`). The token/password is never returned in list API responses.
3. **Deployment attachment:** When deploying, choose the credential from the dropdown or pass `--image-pull-secret ghcr-production`. Crelan attaches `imagePullSecrets: [{name: "ghcr-production"}]` to your application Pod spec, allowing the kubelet to pull the private image securely.
4. **Credential deletion:** Remove credentials anytime via `DELETE /api/v1/registry-credentials/{name}`.

