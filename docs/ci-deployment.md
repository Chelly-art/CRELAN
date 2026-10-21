# Deploying from external CI systems

The Crelan workspace API accepts deployments from any CI provider. The examples
create deployments and poll their status, so the workspace token needs both
`deployments:write` and `deployments:read`. Set `CRELAN_API_URL` and
`CRELAN_TOKEN` as protected CI secrets; do not put the token in the repository.

There are two deployment flows:

1. **Build in CI and deploy an image.** The pipeline checks out its own source,
   builds and pushes an image, then posts the image reference to Crelan. This
   works with GitLab CI, Jenkins, and other CI systems, and is the recommended
   flow for GitLab-hosted source repositories.
2. **Ask Crelan to build source.** Send a repository URL and branch. Crelan
   clones and builds it. At present, this flow accepts GitHub repository URLs;
   private repositories need a connected GitHub App. GitLab source repositories
   must use flow 1 until Crelan adds GitLab repository access.

The examples below call the same deployment API used by the `crelan deploy`
CLI. The CLI itself can also be installed in a runner and used with
`crelan deploy --image ...` or `crelan deploy --repo ...`.

The equivalent CLI commands are:

```sh
# Build and push the image in CI, then deploy it.
crelan deploy --name "$CRELAN_APP_NAME" --image "$IMAGE_TAG" --wait

# Let Crelan clone and build a public GitHub repository.
crelan deploy --name "$CRELAN_APP_NAME" --repo "$CRELAN_SOURCE_REPOSITORY" \
  --branch "${CRELAN_SOURCE_BRANCH:-main}" --wait
```

## GitLab CI

Copy [gitlab-ci.yml](../examples/ci/gitlab-ci.yml) into the application repo as
`.gitlab-ci.yml`. Configure `CRELAN_API_URL`, `CRELAN_TOKEN`, and
`CRELAN_APP_NAME` as CI/CD variables; mark the token masked and protected. Set
`CRELAN_IMAGE_PULL_SECRET` when deploying from a private registry. The image job
uses GitLab's built-in registry and a Docker-in-Docker runner; the runner must
permit privileged Docker services.

The `deploy:source` job is manual and demonstrates Crelan-managed source builds.
Set `CRELAN_SOURCE_REPOSITORY` to a public GitHub URL and optionally
`CRELAN_SOURCE_BRANCH`. For the GitLab project itself, use `deploy:image`.

## Jenkins

Copy [Jenkinsfile](../examples/ci/Jenkinsfile) into the application repo. Add
Jenkins credentials with IDs `crelan-api-url`, `crelan-token`, and
`container-registry`; update the credential IDs if needed. Set the
`CRELAN_APP_NAME` and optional `CRELAN_IMAGE_PULL_SECRET` parameters. The
pipeline builds and pushes the checked-out source by default. Set
`CONTAINER_IMAGE_REPOSITORY` to the image repository and set the `Build in
Crelan from a public GitHub repo` parameter to select the source-build example
instead. The two source parameters configure that repository and branch.

The image deployed to Crelan must be accessible by Crelan. For a private
registry, create a registry credential in the Crelan workspace and pass its
name as `CRELAN_IMAGE_PULL_SECRET` in either example. Public images do not need
a pull secret.
