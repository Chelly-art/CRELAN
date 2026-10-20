# Crelan infrastructure as code

The integrations in this directory manage Crelan workspace deployments through
the versioned REST API:

- [Terraform and OpenTofu provider](terraform-provider-crelan/README.md)
- [Pulumi TypeScript dynamic resource](pulumi/README.md)

Both support image deployments and GitHub source builds. GitLab and other
non-GitHub sources should be built by CI and then deployed as an image. The
Terraform/OpenTofu provider is not published to a public provider registry yet;
the Pulumi sample is a TypeScript dynamic provider rather than a packaged,
multi-language provider.
