# Crelan

Crelan is a small Kubernetes demo to browse pods, ConfigMaps, and Secret metadata, deploy an app from a public GitHub repository, and delete its Kubernetes resources. Secret values are never shown in the browser.

## Connect a local cluster

The dashboard lists cluster contexts configured by the operator in the server's `KUBECONFIG`; users can select an available context but cannot upload credentials. When Crelan runs inside Kubernetes, it uses the pod's ServiceAccount. The deployment form has its own target-cluster selector, and each deployment remembers that target for viewing and deletion. For Minikube:

```sh
minikube start
minikube update-context
kubectl config use-context minikube
kubectl config current-context
```

Create the namespace Crelan uses for its single demo workspace:

```sh
kubectl create namespace crelan-ws-1 --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace crelan-ws-1 capsule.clastix.io/tenant=crelan-ws-1 --overwrite
```

Run Crelan with the same kubeconfig and start the local-only demo server:

```sh
export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
CRELAN_DEMO_LOGIN=true go run ./cmd/server
```

Open <http://localhost:8080/>. Enter any username and password; Crelan does not validate or save them. All demo sign-ins use the same workspace and cluster namespace. The in-memory SQLite store keeps deployment queue state while the server runs; it does not create a database file, and that state resets when the server restarts.

To connect another cluster, point `KUBECONFIG` at its kubeconfig and set its current context to the cluster you want Crelan to use. The kubeconfig identity needs permission to list Pods, ConfigMaps, and Secrets in `crelan-ws-1`, and to create and delete the workload resources Crelan manages. Inside a cluster, grant those permissions to Crelan's ServiceAccount instead.

## Public repository builds

Repository deployments also need an image registry and a `kubernetes.io/dockerconfigjson` Secret in `crelan-ws-1`:

- `CRELAN_IMAGE_REGISTRY`: registry host and optional repository path.
- `CRELAN_REGISTRY_SECRET`: Secret used to push and pull images.
- `CRELAN_KPACK_BUILDER_IMAGE`: kpack builder image for repositories without a Dockerfile.

Repositories with a Dockerfile use rootless BuildKit Jobs. Repositories without one use kpack/Buildpacks and need the `kpack.io/v1alpha2` Build CRD and a compatible builder. Install an Ingress controller and set `CRELAN_APP_BASE_DOMAIN` if deployed apps need public URLs.
