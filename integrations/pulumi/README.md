# Crelan with Pulumi (TypeScript)

This example defines a Pulumi dynamic resource for a Crelan deployment. It can
manage an existing image or a GitHub source repository. The resource stores
its deployment ID and waits for Crelan to report `running` before the update
completes.

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
configured to wait for readiness. Pulumi encrypts the token because it is set
with `--secret`. Keep the Pulumi state backend protected as it stores resource
inputs and outputs.

## Current Pulumi dynamic-provider limits

This starter is TypeScript-only. Pulumi dynamic providers currently do not
support `read`/import, so `pulumi refresh` cannot reconcile an out-of-band
deleted or changed Crelan deployment. Keep the resource in one Pulumi stack and
avoid managing the same Crelan app from multiple stacks. A generated, native
multi-language Pulumi provider can replace this starter when the API surface
and release process are established.
