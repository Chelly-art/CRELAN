# Cluster components for Crelan

This guide describes the cluster-side components for a Crelan installation. The Terraform under `terraform/` currently provisions an AWS EKS cluster; it does not install this full add-on set yet. Install and verify the required components before connecting a production cluster.

## Required for the current deployment flow

| Component | Purpose | Notes |
| --- | --- | --- |
| Capsule | Tenant and namespace boundaries used by Crelan | Crelan checks the `capsule.clastix.io/tenant` namespace label. Install Capsule and provision each workspace tenant/namespace. |
| Ingress controller | Routes traffic to deployed apps | Crelan creates Kubernetes Ingress resources. Configure `CRELAN_INGRESS_CLASS` to match the controller. Traefik is the recommended default; an existing compatible controller can be used. |
| Registry pull access | Lets Kubernetes pull built or customer-provided images | This is credentials or cloud identity, not a cluster add-on. Configure the workspace image pull Secret or provider-specific workload identity. |
| kpack and a builder image | Builds public repositories that do not have a Dockerfile | Install kpack only if using the Buildpacks path; configure `CRELAN_KPACK_BUILDER_IMAGE`. Dockerfile builds use Crelan's rootless BuildKit Jobs and do not need a persistent BuildKit service. |

The cluster identity used by Crelan also needs scoped RBAC to read Pods, ConfigMaps, and Secret metadata, and to create, inspect, and delete the deployment resources in the workspace namespace. Secret values should not be exposed to the UI.

## Recommended workload guardrails

| Component | Purpose | Notes |
| --- | --- | --- |
| Kyverno | Validate and optionally mutate workloads at admission | Use policies to block privileged containers, host namespaces, unsafe hostPath mounts, and unapproved registries; require resource requests/limits and non-root execution. Start in audit mode, review violations, then enforce. Use Kyverno as the policy engine instead of installing Gatekeeper as well. |
| NetworkPolicy-capable CNI | Restricts workload network traffic | Confirm the installed CNI enforces Kubernetes NetworkPolicy; the API objects alone do not filter traffic. Apply tenant-scoped default-deny and explicit allow rules. |
| Pod Security Admission | Built-in Kubernetes baseline/restricted pod controls | Enable the appropriate namespace pod-security labels. This is part of Kubernetes and does not require another controller. |

Kyverno can reject or adjust unsafe manifests, but it does not isolate a running container from the host. Admission policy and runtime sandboxing solve different problems.

## Optional isolation

For workloads that need stronger isolation, configure a sandbox runtime such as gVisor or Kata Containers on the worker nodes, then register a Kubernetes `RuntimeClass` and require it for the applicable tenant workloads. This needs compatible node images, CRI configuration, scheduling capacity, and testing; creating a RuntimeClass object by itself is insufficient. Select a runtime supported by the target cloud and node OS. Firecracker-based isolation likewise requires a compatible runtime integration on the nodes; it is not enabled by Kyverno or by installing a generic shim.

## Optional monitoring

| Component | Purpose | When to add |
| --- | --- | --- |
| kube-prometheus-stack (Prometheus, Alertmanager, Grafana) | Cluster and workload metrics, dashboards, alerts | Add when operators need cluster-wide health and alerting. |
| Loki and Grafana Alloy | Central log collection and search | Add when logs need retention and cross-workload search. Crelan can fetch pod logs from the Kubernetes API without Loki. |
| OpenCost | Namespace/workload cost allocation | Add when customers or operators need Kubernetes cost breakdowns. |
| Falco | Runtime behavior alerts | Add if runtime threat detection is part of the operational response plan; it is not a replacement for policy or sandboxing. |

## Optional platform services

- cert-manager automates certificate issuance and renewal when the ingress/TLS setup needs it.
- ExternalDNS manages DNS records from ingress resources when automated DNS is needed.
- A CSI driver is needed for persistent volumes; use the cloud provider's supported driver.
- Argo CD is optional for GitOps management of these add-ons. It is not required for Crelan to create workloads.

Do not install every optional component by default. Enable the components that match the cluster's provider, tenancy model, and operational requirements, and keep their policies and versions under infrastructure-as-code control.
