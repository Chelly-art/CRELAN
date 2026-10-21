# VM servers

This document defines the first VM server workflow for Crelan. A server is a deployment target that can run container images outside the Kubernetes cluster.

## Customer flow

1. In **Servers**, choose **Create server**.
2. Select **Customer AWS account** or **Crelan managed**.
3. Choose a name, AWS region, instance size, and SSH source CIDR. For a customer account, enter the cross-account role ARN and external ID.
4. Crelan provisions an Amazon Linux 2023 VM, creates an Ed25519 SSH key pair, installs Docker and the Crelan agent, and waits for the agent to register. The private key is returned only once for download.
5. In **Servers**, deploy a public container image to a ready server. Source builds can be added after the build pipeline can publish an image for VM targets.
6. Crelan shows deployment status, URL, recent logs, and server health. Server actions include stop, start, and delete.

## Provisioning modes

### Customer AWS account

The customer connects AWS once by creating a narrowly scoped cross-account IAM role for Crelan. Crelan assumes the role with an external ID and short-lived credentials. Store the role ARN as workspace connection metadata; never ask for an IAM user's access key and secret. Provision and bill the EC2 resources in the customer's account.

### Crelan managed

Crelan uses its AWS workload identity to create EC2 resources in a dedicated Crelan account. Track instance, disk, public IPv4, region, and lifecycle state against the workspace. Crelan pays AWS and must apply plan limits and usage billing before making this option generally available.

## Agent and deployment contract

Install a small system service on every VM. It opens an outbound TLS connection to Crelan, authenticates with a one-time enrollment token, exchanges that token for a revocable server credential, and then polls or streams desired state. It must not require inbound access from the Crelan control plane.

The control plane stores server and deployment records scoped to the workspace. The agent reports heartbeat, Docker availability, deployment state, public endpoint, and bounded log batches. Deployment commands include an immutable image reference, port, and environment/secret payload. Secrets are returned only to the authenticated target when needed and must never be included in normal API responses or audit details.

For an initial image deployment, the agent pulls the image, replaces the named container, publishes only the requested application port, and reports ready or failed. Each command has an ID so retries are idempotent. A deployment timeout leaves the previous healthy container in place where possible.

## SSH and networking

EC2 creates an Ed25519 key pair and returns the private key to Crelan in the create response. Crelan must not log or persist the private key; the UI offers it once as a download and discards its in-memory copy after download. AWS itself does not keep a recoverable copy. Never open SSH to `0.0.0.0/0`; ask for a restricted source CIDR or use Systems Manager Session Manager and omit inbound SSH.

Use an AWS-provided Amazon Linux 2023 AMI initially. Resolve an AMI per region and architecture at provisioning time. A custom AMI is a later optimization for faster startup and a pinned software baseline, not a first-release requirement.

## Required records and APIs

- `servers`: workspace, name, provider mode, AWS region, instance ID, public endpoint, agent credential hash, status, and timestamps.
- `server_commands`: server, command ID, requested image/configuration, state, status message, and timestamps.
- Customer APIs: create/list/get/stop/start/delete servers; create VM-target deployment; list deployment history and logs.
- Agent APIs: enroll once; heartbeat; claim next command; report command progress/result; upload bounded logs.

Every customer endpoint must scope reads and writes by the authenticated workspace. Agent endpoints authenticate by a server-scoped credential, not a workspace API token. Revoke the agent credential when a server is deleted or disconnected.

## Delivery sequence

1. Add VM records, workspace-scoped server APIs, and the agent protocol.
2. Add EC2 provisioning for both modes, including key handling, subnet/security-group creation, quotas, and cleanup on partial failure.
3. Add VM-target image deployment and dashboard status/log views.
4. Add pricing/usage controls for Crelan-managed servers and operational alerts for unreachable agents.

