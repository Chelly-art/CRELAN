# Crelan

Crelan provides authenticated customer workspaces for deploying GitHub repositories or existing container images, inspecting app-scoped logs and resources, and provisioning PostgreSQL or MySQL clusters. Platform administrators have a separate, allowlisted operations view. Secret values are never shown in resource metadata.

## Kubernetes access & cloud persistence

Platform administrators can connect the deployment cluster either via environment variables (recommended for cloud hosting environments) or from the internal admin page at `/assets/admin.html`.

### Automatic cluster connection on cloud platforms (Cloud, Render, Kubernetes)
When running Crelan on containerized cloud platforms where filesystem state may reset on redeployment, pass your kubeconfig via an environment variable. Crelan automatically parses, validates, and activates the cluster on every service startup:

```sh
# Generate the base64-encoded kubeconfig
base64 -w0 kubeconfig.yaml
```

Set the output as `CRELAN_KUBECONFIG_DATA_B64` (or set `CRELAN_KUBECONFIG_DATA` with the raw YAML) in your host environment variables.

### Admin UI cluster connection
Platform administrators can also connect or switch clusters interactively from `/assets/admin.html`. Paste a self-contained kubeconfig, select its context, and connect. Crelan validates Kubernetes API access before making that cluster active. Customer sessions never receive or see the kubeconfig.

To persist an admin-connected cluster across restarts when using SQLite, configure `DATABASE_PATH` on persistent storage and set `CRELAN_KUBECONFIG_ENCRYPTION_KEY` to a 32-byte base64-encoded key:

```sh
openssl rand -base64 32
```
Crelan encrypts stored kubeconfigs with AES-256-GCM. A secure fallback encryption key is used if `CRELAN_KUBECONFIG_ENCRYPTION_KEY` is not provided, ensuring credentials remain encrypted at rest.

### Cluster requirements & tenant isolation
Each Crelan workspace maps to a Capsule Tenant and a workspace namespace named `crelan-ws-<workspace-id>`. If that namespace does not exist, Crelan creates the Capsule Tenant and namespace on first provisioning. Configure `CRELAN_CAPSULE_TENANT_OWNER` with the Capsule user name that should own the tenant. The cluster must have Capsule's `capsule.clastix.io/v1beta2` Tenant CRD installed, and Crelan's identity needs permission to create/get Tenants and Namespaces. Existing namespaces must carry the matching `capsule.clastix.io/tenant=crelan-ws-<workspace-id>` label.

For local development, point Crelan at a kubeconfig that contains the cluster contexts you want to make available:

```sh
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
CRELAN_DEMO_LOGIN=true go run ./cmd/server
```

Open <http://localhost:8080/>. In development login mode, any username and password are accepted. Source runs default to in-memory SQLite; set `DATABASE_PATH` to a protected file for persistence. The container image defaults to `/data/crelan.db`. Mount `/data` on durable storage.

Set `CRELAN_PLATFORM_ADMIN_EMAILS` to a comma-separated list of platform administrator email addresses. Admin access requires a signed-in browser session, a listed email, and a linked GitHub identity with a verified primary email; a password signup alone cannot claim platform admin access. Customer app APIs are scoped to the authenticated workspace and application labels.

The cluster identity needs permissions for the Capsule Tenant/Namespace lifecycle, app Deployments/Services/Pods/ConfigMaps/Secrets/Jobs, logs, NetworkPolicies, ResourceQuotas, and Ingresses if enabled.

## Deployments & Workloads

Crelan supports both source-based builds and pre-built container image rollouts:

1. **Source builds (GitHub):**
   - **Build mechanisms:** Select `auto` (auto-detects Dockerfile, falls back to Buildpack), `dockerfile` (rootless BuildKit job), or `buildpack` (Cloud Native Buildpacks via kpack).
   - **✨ Built-in Dockerfile Generator:** Built directly into the dashboard. Generates optimized, multi-stage production Dockerfiles for Node.js, Python, Go, Rust, Java/Spring Boot, and Static Web/SPA with non-root security best practices.
   - **Private repositories:** Supported via connected GitHub App (`/settings/github/install`) with webhook triggers on `git push`, or by providing a Personal Access Token (`ghp_...`).
2. **Container image deployment:**
   - Deploy pre-built images from Docker Hub, GitHub Container Registry (`ghcr.io`), GitLab Registry, AWS ECR, or custom registries.
   - Save private registry credentials directly in the dashboard or via API; credentials are encrypted as Kubernetes `imagePullSecrets` (`kubernetes.io/dockerconfigjson`) in the tenant namespace.
3. **Configuration & Secrets:**
   - Inject environment variables (ConfigMaps) and sensitive secrets (Kubernetes Secrets) at deploy time or runtime via `PUT /api/v1/deployments/{id}/configuration`.
   - Update triggers an automated zero-downtime rolling update.
4. **Custom Domains & SSL:**
   - Attach custom domains with automated DNS verification (`_crelan-verify.<host>` TXT record) and automated TLS certificate provisioning.
5. **Rollbacks & Pod Events:**
   - One-click rollback in the UI history panel or via `POST /api/v1/deployments/{id}/rollback` to instantly revert to any previous successful run.
   - Real-time Kubernetes warning event detection (`CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`) via `GET /api/v1/deployments/{id}/events`.
6. **Persistent storage:**
   - Attach durable volumes with configurable mount paths. Unmounted volumes are retained on app deletion to prevent accidental data loss.

App Deployments default to one replica. Set `CRELAN_APP_REPLICAS` (1–5) to use a fixed replica count for new rollouts. Set `CRELAN_APP_HEALTH_CHECK_PATH` to a common HTTP path (for example `/healthz`) to add readiness, startup, and liveness probes for all apps.

## Managed databases

The dashboard can provision a three-instance PostgreSQL cluster using CloudNativePG or a three-instance MySQL InnoDB Cluster using the Oracle MySQL Operator. Install the chosen operator and CRDs before enabling that engine. The Crelan identity needs namespaced permissions to create, read, list, and delete the corresponding custom resources and Secrets. Database credentials are generated and shown only in the create response; save them at that time. MySQL's generated root credential stays in a Kubernetes Secret; Crelan creates a separate `app` account with access to the `app` database and returns that account's credentials.

Set all of `CRELAN_DB_BACKUP_BUCKET`, `CRELAN_DB_BACKUP_REGION`, `CRELAN_DB_BACKUP_ACCESS_KEY`, and `CRELAN_DB_BACKUP_SECRET_KEY` to enable scheduled object-storage backups for newly created databases. Set `CRELAN_DB_BACKUP_ENDPOINT` for an S3-compatible service. CNPG uses daily base backups and WAL archiving with 30-day retention; MySQL uses a daily operator backup schedule. Existing databases are not changed retroactively. Use a least-privilege bucket identity, enable Kubernetes Secret encryption at rest, and validate actual backup completion and a restore on the target cluster before offering an RPO/RTO. Customers can view and trigger backups when the installed operator schema supports the workflow. Crelan supports restore-as-new for CloudNativePG from the latest completed backup; MySQL restore remains operator-led. A target time, when supplied through the API, must be verified against the operator's recovery behavior. Backups and restore are disabled when the expected CRD schema is unavailable.

The service enforces a limit of five databases and 500Gi aggregate logical database storage per workspace, with a 100Gi maximum per database. Each database currently requests three PVCs, so the physical storage quota is 1,500Gi at the maximum plan limit. A namespace ResourceQuota provides a Kubernetes-side storage, PVC, and pod guardrail. The platform admin page includes a readiness checklist for cluster API access, Capsule ownership mapping, operator APIs and schemas, storage classes, ready node count, and failure zones. API discovery does not prove that an operator is healthy, that its images can be pulled, or that restore works; check those on the connected cluster. NetworkPolicies restrict database ingress to pods in the same workspace namespace and configured operator namespaces. Set `CRELAN_CLOUDNATIVEPG_NAMESPACE` or `CRELAN_MYSQL_OPERATOR_NAMESPACE` if your operators run outside the defaults `cnpg-system` or `mysql-operator`. Policies require a CNI that enforces NetworkPolicy.

Database Services are `ClusterIP` only. Apps in the same workspace use the cluster DNS endpoint shown in the dashboard. External database access, managed client TLS certificates, and per-customer IP allowlists are not configured.

Workspace owners can use **Team** in the customer dashboard to create a one-time developer/viewer invite token, change member roles, and remove members. The token is shown once and must be shared out of band; email delivery is not configured. Sessions can switch between workspaces the user belongs to.

The database operators and Capsule provide orchestration and tenant boundaries, but HA still depends on durable storage, multiple failure zones, operators that are running, and a CNI that enforces NetworkPolicy. Review the admin readiness checklist and run a full restore exercise before promising a recovery objective.

Valkey is an experimental preview, and service-level backups for Valkey and RabbitMQ are not configured. Do not promise recovery objectives for those services; maintain an operator-supported external backup procedure and verify restore before production use. App PVC retention is not a backup.

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

## Enterprise Operations Suite (Advanced Kubernetes Operations)

Crelan embeds a complete enterprise capability set directly into the platform web console without requiring desktop software or external subscription licenses:

1. **Container Image Vulnerability Scanner (CVE Radar):** Real-time image scanning across active pods and deployments. Detects CVE vulnerabilities with CVSS scores, affected packages, and fixed-in versions (`GET /api/v1/admin/security/image-vulnerabilities`).
2. **Helm Release Lifecycle Manager:** In-cluster discovery of Helm v3 releases stored in Kubernetes secrets. Includes a values inspector (`values.yaml`), revision history, one-click rollback (`POST /api/v1/admin/helm/releases/{ns}/{name}/rollback`), and uninstallation (`DELETE /api/v1/admin/helm/releases/{ns}/{name}`).
3. **Custom Resource Definitions (CRDs) & Operator Browser:** Deep schema exploration and dynamic instance querying for installed operators (`apiextensions.k8s.io/v1`) with instant YAML/JSON definition viewers (`GET /api/v1/admin/crds`).
4. **Interactive RBAC Permission Inspector (`can-i`):** Visual "Who Can Do What" permission evaluation across users, groups, and service accounts using live `SelfSubjectAccessReview` and local RBAC engine fallback (`POST /api/v1/admin/security/can-i`).
5. **Multi-Cluster Fleet Dashboard:** Centralized fleet health monitoring across EKS, GKE, K3s, and Managed Cloud clusters with seamless context switching (`GET /api/v1/admin/clusters/fleet`, `POST /api/v1/admin/clusters/switch-context`).
6. **Quick Launcher Command Palette (`Cmd+K` / `Ctrl+K`):** Global fuzzy search across all cluster resources, workloads, nodes, and administrative actions for instant keyboard navigation.

## Environment Variables Reference

| Variable | Description | Default / Example |
|---|---|---|
| `CRELAN_KUBECONFIG_DATA_B64` | Base64-encoded kubeconfig YAML for automatic cluster connection | Recommended for cloud hosting |
| `CRELAN_KUBECONFIG_DATA` | Raw kubeconfig YAML string | Alternative to `_B64` |
| `CRELAN_KUBECONFIG_ENCRYPTION_KEY` | 32-byte base64 AES key for encrypting stored kubeconfigs in SQLite | `openssl rand -base64 32` |
| `DATABASE_PATH` | Path to persistent SQLite database file | `/data/crelan.db` (or in-memory if empty) |
| `CRELAN_PLATFORM_ADMIN_EMAILS` | Comma-separated allowlist of platform admin email addresses | `admin@example.com` |
| `CRELAN_APP_BASE_DOMAIN` | Base domain for customer application URLs | `apps.crelan.example.com` |
| `CRELAN_APP_REPLICAS` | Default replica count for deployed applications (1-5) | `1` |
| `CRELAN_APP_HEALTH_CHECK_PATH` | Common HTTP health check path for liveness/readiness probes | `/healthz` |
| `CRELAN_IMAGE_REGISTRY` | Default image registry host for source builds | `ghcr.io/myorg` |
| `CRELAN_REGISTRY_SECRET` | Platform registry push/pull secret | `crelan-registry-creds` |
| `CRELAN_KPACK_BUILDER_IMAGE` | Cloud Native Buildpacks builder image | `gcr.io/buildpacks/builder:v1` |
| `CRELAN_CAPSULE_TENANT_OWNER` | Capsule tenant owner identity | `crelan-system` |
| `CRELAN_DEMO_LOGIN` | Enable mock login mode for local dev (binds to loopback) | `false` |

