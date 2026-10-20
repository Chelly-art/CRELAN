# Crelan

Crelan lets users browse Pods, ConfigMaps, and Secret metadata, deploy apps from public GitHub repositories, and delete the Kubernetes resources it creates. Secret values are never shown in the browser.

## Kubernetes access

Kubeconfig credentials are configured by the operator and cannot be uploaded through the UI. When Crelan runs outside Kubernetes, it lists contexts from `CRELAN_KUBECONFIG_DATA_B64` (a base64-encoded kubeconfig secret), `CRELAN_KUBECONFIG_DATA` (raw kubeconfig YAML), or, if neither is set, the server process's `KUBECONFIG` file. When Crelan runs inside Kubernetes without either setting, it uses its pod's ServiceAccount. Users choose an available context in the dashboard, and each deployment remembers its selected cluster for status checks and deletion.

For a hosted Crelan instance whose environment editor accepts key/value pairs, add `CRELAN_KUBECONFIG_DATA_B64` as the key and a single-line base64 value as the value. This avoids multiline input. Restart/redeploy Crelan after changing it. Do not put the kubeconfig in the repository or a public build argument. Lens normally uses the same kubeconfig as `kubectl`. Check the context names and create a flattened local copy (which embeds referenced certificate files), then encode it as one line:

```sh
kubectl config get-contexts
kubectl config view --raw --flatten > /tmp/crelan-kubeconfig.yaml
base64 < /tmp/crelan-kubeconfig.yaml | tr -d '\n' > /tmp/crelan-kubeconfig.b64
```

Copy the contents of `/tmp/crelan-kubeconfig.b64` into the environment variable's value field. Base64 is an encoding, not encryption, so treat the value as a credential and keep it private. The kubeconfig's API servers must be reachable from the hosted Crelan service. Contexts that authenticate through a local `exec` plugin (for example, cloud CLI login helpers) will not work in the standard Crelan container unless that plugin and its credentials are also configured there; use credentials supported directly by kubeconfig or extend the server image for that provider.

Crelan expects the workspace namespace `crelan-ws-1` to exist in each cluster and to carry the tenant label used by the deployment policy. Create it in each cluster before connecting Crelan:

```sh
kubectl create namespace crelan-ws-1 --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace crelan-ws-1 capsule.clastix.io/tenant=crelan-ws-1 --overwrite
```

For local development, point Crelan at a kubeconfig that contains the cluster contexts you want to make available:

```sh
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
CRELAN_DEMO_LOGIN=true go run ./cmd/server
```

Open <http://localhost:8080/>. In development login mode, any username and password are accepted. The in-memory SQLite store keeps deployment queue state while the server runs; it does not create a database file, and that state resets when the server restarts.

The identity used by each configured context needs permission to list Pods, ConfigMaps, and Secrets in `crelan-ws-1`, and to create and delete the workload resources Crelan manages. For an in-cluster installation, grant those permissions to Crelan's ServiceAccount.

## Public repository builds

Repository deployments also need an image registry and a `kubernetes.io/dockerconfigjson` Secret in `crelan-ws-1`:

- `CRELAN_IMAGE_REGISTRY`: registry host and optional repository path.
- `CRELAN_REGISTRY_SECRET`: Secret used to push and pull images.
- `CRELAN_KPACK_BUILDER_IMAGE`: kpack builder image for repositories without a Dockerfile.

Repositories with a Dockerfile use rootless BuildKit Jobs. Repositories without one use kpack/Buildpacks and need the `kpack.io/v1alpha2` Build CRD and a compatible builder. Install an Ingress controller and set `CRELAN_APP_BASE_DOMAIN` if deployed apps need public URLs.

## CLI and GitHub Actions

The `crelan` CLI automates deployment workflows against the Crelan API. Build it with:

```sh
go build -o crelan ./cmd/crelan
```

Set `CRELAN_API_URL` and `CRELAN_TOKEN` to the Crelan URL and a workspace API token with the required scopes, then use commands such as:

```sh
./crelan deploy --name storefront --repo https://github.com/example/storefront --branch main --wait
./crelan deployments
./crelan status 1
./crelan logs 1
./crelan delete 1
```

The composite GitHub Action at `.github/actions/deploy` runs the CLI in a workflow. Configure `CRELAN_API_URL` as a repository variable and `CRELAN_TOKEN` as a repository secret, then adapt [examples/crelan-deploy.yml](examples/crelan-deploy.yml) to your repository and action release. Private source repositories require a connected GitHub App.

## Other CI systems, IaC, and interactive CLI

GitLab CI and Jenkins can build and push a container image, then deploy it with `crelan deploy --name APP --image IMAGE`. `--repo` asks Crelan to build a GitHub repository; use the image flow for repositories hosted elsewhere. See [CI deployment examples](docs/ci-deployment.md), [GitLab CI](examples/ci/gitlab-ci.yml), and [Jenkins](examples/ci/Jenkinsfile).

Run `crelan tui` for an interactive menu to list, deploy, inspect, and delete applications. For infrastructure as code, see the [Terraform and OpenTofu provider](integrations/terraform-provider-crelan/README.md) and [Pulumi TypeScript example](integrations/pulumi/README.md). The Terraform provider is currently installed from a local build; the Pulumi dynamic resource is TypeScript-only and does not support import or read reconciliation.
