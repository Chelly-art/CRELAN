# Crelan with Pulumi (TypeScript)

This package defines a Pulumi dynamic resource for a Crelan deployment. It can
manage an existing image or a GitHub source repository. The resource stores
its deployment ID and waits for Crelan to report `running` before the update
completes. The package is prepared for npm publication, but has not been
published yet; use this project locally or pack it with `npm pack`.

## Configure and run

Use Node.js and npm (dynamic providers in TypeScript are not compatible with
pnpm). In this directory:

```sh
npm install
pulumi stack init dev
pulumi config set crelan:apiUrl https://crelan.example.com
pulumi config set --secret crelan:token '<workspace-token>'
pulumi config set crelan:appName storefront
pulumi config set crelan:image ghcr.io/acme/storefront:1.4.2
pulumi up
```

The example uses an image. To let Crelan build source instead, clear
`crelan:image` with `pulumi config rm crelan:image` and set
`crelan:repositoryUrl` to a GitHub URL; optionally set `crelan:branch`. Private
GitHub repositories need a connected GitHub App.

Use a token with `deployments:write` and `deployments:read` when the resource is
configured to wait for readiness. The resource retains the token for delete
operations and explicitly marks that output as secret. Keep the Pulumi state
backend protected as it stores resource inputs and outputs.

Run `npm test`, `npm run build`, and `npm pack --dry-run` before publishing.
After the package owner and npm publishing credentials are configured, publish
with `npm publish --access public`; consumers can then import `Deployment` from
`crelan-pulumi-provider`.

## Current Pulumi dynamic-provider limits

This dynamic provider is TypeScript-only. Dynamic providers do not support
`read`/import, so `pulumi refresh` cannot reconcile an out-of-band deleted or
changed Crelan deployment. Keep the resource in one Pulumi stack and avoid
managing the same Crelan app from multiple stacks. A native or bridged Pulumi
provider is needed for import, refresh reconciliation, and other Pulumi
languages. See [Pulumi's dynamic provider limits](https://www.pulumi.com/docs/iac/concepts/providers/dynamic-providers/).
