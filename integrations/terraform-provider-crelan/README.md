# Crelan Terraform and OpenTofu provider

This provider manages Crelan workspace deployments through the Crelan REST API.
It supports `crelan_deployment` resources for existing images and GitHub source
repositories. OpenTofu can use the same provider binary and configuration.

## Build locally

```sh
cd integrations/terraform-provider-crelan
go build -o terraform-provider-crelan .
```

Install the binary into the local filesystem mirror and initialize the example
using that mirror:

```sh
make install-local
cd examples/image
terraform init -plugin-dir="$HOME/.terraform.d/plugins"
terraform plan
terraform apply
```

OpenTofu uses the same provider protocol and mirror layout:

```sh
tofu init -plugin-dir="$HOME/.terraform.d/plugins"
tofu plan
tofu apply
```

For faster provider iteration, Terraform and OpenTofu also support local
development overrides. Development overrides and filesystem mirrors are
local-only; normal users need a signed release in a Terraform or OpenTofu
provider registry.

## Configure credentials

Set `CRELAN_API_URL` to the instance base URL and `CRELAN_TOKEN` to a workspace
token. The provider accepts these from the environment so credentials need not
be placed in HCL. Use a token with `deployments:write` and `deployments:read`;
the provider reads deployment state during refresh and when waiting for
readiness.

```sh
export CRELAN_API_URL="https://crelan.example.com"
export CRELAN_TOKEN="<workspace-token>"
```

## Resource fields

Set exactly one of `image` or `repository_url`. Image deployments may use
`image_pull_secret` to reference a saved registry credential. Repository builds
currently support GitHub URLs; private repositories need a connected GitHub
App. Updates queue a new deployment. Destroy deletes the managed app.

The `wait_timeout_seconds` default is 2700 seconds. Outputs include
`deployment_id`, `status`, and `url`.

See `examples/image` and `examples/source` for configurations. The provider is
not published to a registry yet; its `crelan/crelan` source address is reserved
for the initial release.
