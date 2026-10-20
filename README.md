# Crelan

Crelan provides authenticated customer workspaces for deploying GitHub repositories or existing container images, inspecting app-scoped logs and resources, and provisioning PostgreSQL or MySQL clusters. Platform administrators have a separate, allowlisted operations view. Secret values are never shown in resource metadata.

## Kubernetes access

Kubeconfig credentials are configured by the operator and cannot be uploaded through the UI. When Crelan runs outside Kubernetes, it reads `CRELAN_KUBECONFIG_DATA_B64`, `CRELAN_KUBECONFIG_DATA`, or the server process's `KUBECONFIG` file. In-cluster Crelan uses its ServiceAccount. The configured cluster is the platform's cluster; customer browser sessions do not receive Kubernetes credentials.

For a hosted Crelan instance whose environment editor accepts key/value pairs, add `CRELAN_KUBECONFIG_DATA_B64` as the key and a single-line base64 value as the value. This avoids multiline input. Restart/redeploy Crelan after changing it. Do not put the kubeconfig in the repository or a public build argument. Lens normally uses the same kubeconfig as `kubectl`. Check the context names and create a flattened local copy (which embeds referenced certificate files), then encode it as one line:

```sh
kubectl config get-contexts
kubectl config view --raw --flatten > /tmp/crelan-kubeconfig.yaml
base64 < /tmp/crelan-kubeconfig.yaml | tr -d '\n' > /tmp/crelan-kubeconfig.b64
```

Copy the contents of `/tmp/crelan-kubeconfig.b64` into the environment variable's value field. Base64 is an encoding, not encryption, so treat the value as a credential and keep it private. The kubeconfig's API servers must be reachable from the hosted Crelan service. Contexts that authenticate through a local `exec` plugin (for example, cloud CLI login helpers) will not work in the standard Crelan container unless that plugin and its credentials are also configured there; use credentials supported directly by kubeconfig or extend the server image for that provider.

Each Crelan workspace maps to a Capsule Tenant and a workspace namespace named `crelan-ws-<workspace-id>`. If that namespace does not exist, Crelan creates the Capsule Tenant and namespace on first provisioning. Configure `CRELAN_CAPSULE_TENANT_OWNER` with the Capsule user name that should own the tenant. The cluster must have Capsule's `capsule.clastix.io/v1beta2` Tenant CRD installed, and Crelan's identity needs permission to create/get Tenants and Namespaces. Existing namespaces must carry the matching `capsule.clastix.io/tenant=crelan-ws-<workspace-id>` label.

For local development, point Crelan at a kubeconfig that contains the cluster contexts you want to make available:

```sh
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
CRELAN_DEMO_LOGIN=true go run ./cmd/server
```

Open <http://localhost:8080/>. In development login mode, any username and password are accepted. Source runs default to in-memory SQLite; set `DATABASE_PATH` to a protected file for persistence. The container image defaults to `/data/crelan.db`. Mount `/data` on durable storage. Platform admins can download a consistent SQLite snapshot from the admin page; store it off-cluster on a schedule. The current control plane is a single-instance SQLite deployment; do not scale Crelan replicas against a shared SQLite file.

Set `CRELAN_PLATFORM_ADMIN_EMAILS` to a comma-separated list of platform administrator email addresses. Admin access requires a signed-in browser session, a listed email, and a linked GitHub identity with a verified primary email; a password signup alone cannot claim platform admin access. A workspace API token cannot grant platform access. Customer app APIs are scoped to the authenticated workspace and application labels.

The cluster identity needs permissions for the Capsule Tenant/Namespace lifecycle, app Deployments/Services/Pods/ConfigMaps/Secrets/Jobs, logs, NetworkPolicies, ResourceQuotas, and Ingresses if enabled. Database provisioning also requires access to namespace Secrets, Jobs, NetworkPolicies, ResourceQuotas, and the enabled engine custom resources. Keep customer write operations behind Crelan's APIs and tenant mapping.

## Public repository builds

Repository deployments also need an image registry and a `kubernetes.io/dockerconfigjson` Secret in `crelan-ws-1`:

- `CRELAN_IMAGE_REGISTRY`: registry host and optional repository path.
- `CRELAN_REGISTRY_SECRET`: Secret used to push and pull images.
- `CRELAN_KPACK_BUILDER_IMAGE`: kpack builder image for repositories without a Dockerfile.

Repositories with a Dockerfile use rootless BuildKit Jobs. Repositories without one use kpack/Buildpacks and need the `kpack.io/v1alpha2` Build CRD and a compatible builder. Install an Ingress controller and set `CRELAN_APP_BASE_DOMAIN` if deployed apps need public URLs.

Existing images can be deployed from the workspace dashboard or with `crelan deploy --name api --image ghcr.io/acme/api:1.0`. Public images work directly. The workspace dashboard can also save a private registry credential as a Kubernetes `dockerconfigjson` Secret and attach it to image deployments. The credential password is not stored in Crelan's SQL database and is never returned by the list API. `CRELAN_REGISTRY_SECRET` remains available for a platform-wide registry used by source builds.

App Deployments default to one replica. Set `CRELAN_APP_REPLICAS` (1–5) to use a fixed replica count for new rollouts. Set `CRELAN_APP_HEALTH_CHECK_PATH` to a common HTTP path (for example `/healthz`) to add readiness, startup, and liveness probes for all apps. This is a platform-wide setting; per-app autoscaling and configurable probes are not available yet.

## Managed databases

The dashboard can provision a three-instance PostgreSQL cluster using CloudNativePG or a three-instance MySQL InnoDB Cluster using the Oracle MySQL Operator. Install the chosen operator and CRDs before enabling that engine. The Crelan identity needs namespaced permissions to create, read, list, and delete the corresponding custom resources and Secrets. Database credentials are generated and shown only in the create response; save them at that time. MySQL's generated root credential stays in a Kubernetes Secret; Crelan creates a separate `app` account with access to the `app` database and returns that account's credentials.

Set all of `CRELAN_DB_BACKUP_BUCKET`, `CRELAN_DB_BACKUP_REGION`, `CRELAN_DB_BACKUP_ACCESS_KEY`, and `CRELAN_DB_BACKUP_SECRET_KEY` to enable scheduled object-storage backups for newly created databases. Set `CRELAN_DB_BACKUP_ENDPOINT` for an S3-compatible service. CNPG uses daily base backups and WAL archiving with 30-day retention; MySQL uses a daily operator backup schedule. Existing databases are not changed retroactively. Use a least-privilege bucket identity, enable Kubernetes Secret encryption at rest, and validate actual backup completion and a restore on the target cluster before offering an RPO/RTO. Restore is currently an operator-led recovery procedure; Crelan does not yet expose a customer restore action.

The service enforces a limit of five databases and 500Gi aggregate logical database storage per workspace, with a 100Gi maximum per database. Each database currently requests three PVCs, so the physical storage quota is 1,500Gi at the maximum plan limit. A namespace ResourceQuota provides a Kubernetes-side storage, PVC, and pod guardrail. The platform admin page includes a readiness checklist for cluster API access, Capsule, both database APIs, storage class, node count, and zone count. API discovery does not prove that an operator is healthy, that its images can be pulled, or that restore works; check those on the connected cluster. NetworkPolicies restrict database ingress to pods in the same workspace namespace and configured operator namespaces. Set `CRELAN_CLOUDNATIVEPG_NAMESPACE` or `CRELAN_MYSQL_OPERATOR_NAMESPACE` if your operators run outside the defaults `cnpg-system` or `mysql-operator`. Policies require a CNI that enforces NetworkPolicy.

Database Services are `ClusterIP` only. Apps in the same workspace use the cluster DNS endpoint shown in the dashboard. External database access, managed client TLS certificates, and per-customer IP allowlists are not configured.

Workspace owners can use **Team** in the customer dashboard to create a one-time developer/viewer invite token, change member roles, and remove members. The token is shown once and must be shared out of band; email delivery is not configured. Sessions can switch between workspaces the user belongs to.

The database operators and Capsule provide orchestration and tenant boundaries, but HA still depends on durable storage, multiple failure zones, operators that are running, and a CNI that enforces NetworkPolicy. Review the admin readiness checklist and run a full restore exercise before promising a recovery objective.

## CLI and GitHub Actions

The `crelan` CLI automates deployment workflows against the Crelan API. Build it with:

```sh
go build -o crelan ./cmd/crelan
```

Set `CRELAN_API_URL` and `CRELAN_TOKEN` to the Crelan URL and a workspace API token with the required scopes, then use commands such as:

```sh
./crelan deploy --name storefront --repo https://github.com/example/storefront --branch main --wait
./crelan deploy --name api --image ghcr.io/example/api:1.0 --image-pull-secret ghcr-production --port 8080 --wait
./crelan deployments
./crelan status 1
./crelan logs 1
./crelan delete 1
```

The composite GitHub Action at `.github/actions/deploy` runs the CLI in a workflow. Configure `CRELAN_API_URL` as a repository variable and `CRELAN_TOKEN` as a repository secret, then adapt [examples/crelan-deploy.yml](examples/crelan-deploy.yml) to your repository and action release. Private source repositories require a connected GitHub App.
