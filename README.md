# Crelan

Crelan lets users browse Pods, ConfigMaps, and Secret metadata, deploy apps from public GitHub repositories, and delete the Kubernetes resources it creates. Secret values are never shown in the browser.

## Kubernetes access

Kubeconfig credentials are configured by the operator and cannot be uploaded through the UI. When Crelan runs outside Kubernetes, it lists contexts from `CRELAN_KUBECONFIG_DATA` (a kubeconfig YAML secret) or, if unset, from the server process's `KUBECONFIG` file. When Crelan runs inside Kubernetes without either setting, it uses its pod's ServiceAccount. Users choose an available context in the dashboard, and each deployment remembers its selected cluster for status checks and deletion.

For a hosted Crelan instance, add `CRELAN_KUBECONFIG_DATA` as a private, multiline environment variable in the hosting provider's runtime settings, using a kubeconfig that contains both cluster contexts. Restart/redeploy Crelan after changing it. Do not put the kubeconfig in the repository or a public build argument. Lens normally uses the same kubeconfig as `kubectl`. Check the context names and create a flattened local copy (which embeds referenced certificate files) without printing credentials into the terminal:

```sh
kubectl config get-contexts
kubectl config view --raw --flatten > /tmp/crelan-kubeconfig.yaml
```

Then copy `/tmp/crelan-kubeconfig.yaml` directly into the hosting provider's secret environment variable. The kubeconfig's API servers must be reachable from the hosted Crelan service. Contexts that authenticate through a local `exec` plugin (for example, cloud CLI login helpers) will not work in the standard Crelan container unless that plugin and its credentials are also configured there; use credentials supported directly by kubeconfig or extend the server image for that provider.

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
