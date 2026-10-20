# Crelan

Crelan is a small Kubernetes demo with three tasks:

- Browse pods, ConfigMaps, and Secret metadata in the signed-in workspace's Kubernetes namespace.
- Deploy an application from a public GitHub repository.
- Delete a deployed application and its Kubernetes resources.

Secret values are never returned to the browser.

## Run locally

Requires Go 1.26 or newer and a Kubernetes cluster available through `KUBECONFIG` or in-cluster credentials.

```sh
go run ./cmd/server
```

Open <http://localhost:8080/> and create an account. The server stores account and deployment records in `crelan.db`; set `DATABASE_PATH` to use another location.

## Cluster setup

Crelan uses its Kubernetes identity to read resources and create/delete workloads. Each account workspace needs a pre-created namespace and Capsule tenant named `crelan-ws-<workspace-id>` by default. The namespace must be labeled `capsule.clastix.io/tenant=<tenant-name>`. Set `CRELAN_WORKSPACE_NAMESPACE_PREFIX` or `CRELAN_CAPSULE_TENANT_PREFIX` to change the prefixes.

For repository deployments, configure an image registry and a `kubernetes.io/dockerconfigjson` Secret in each workspace namespace:

- `CRELAN_IMAGE_REGISTRY`: registry host and optional repository path.
- `CRELAN_REGISTRY_SECRET`: namespace Secret used to push and pull images.
- `CRELAN_KPACK_BUILDER_IMAGE`: kpack builder image for repositories without a Dockerfile.

The cluster needs Capsule, kpack with the `kpack.io/v1alpha2` Build CRD, a compatible builder, and an Ingress controller if apps need public URLs. Repositories with a Dockerfile use rootless BuildKit Jobs. Apps without a Dockerfile use kpack/Buildpacks. The optional `CRELAN_APP_BASE_DOMAIN` configures app URLs.

Crelan needs permission to list Pods, ConfigMaps, and Secrets in workspace namespaces and manage the workload resources it creates. Secret values are only read by the backend when configuring an app; the cluster browser displays metadata only.

The app uses SQLite and is intended to run as one server replica. A live cluster smoke test requires cluster credentials and has not been performed here.
