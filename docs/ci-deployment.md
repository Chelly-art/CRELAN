# Deploying from external CI systems

The Crelan workspace API accepts deployments from any CI/CD provider. CI pipelines authenticate using scoped workspace API tokens (`crl_...`).

## Generating a workspace token

You can generate tokens in two ways:

1. **Dashboard UI (Recommended):**
   - Go to your workspace dashboard and click the **API Tokens** tab.
   - Enter a token name (e.g., `ci-deploy-token` or `github-actions`).
   - Select expiration (`7 days`, `30 days`, `90 days`, `1 year`, or `No expiration`).
   - Check the required scopes:
     - `deployments:write`: Create, configure, cancel, delete, or roll back apps.
     - `deployments:read`: Check deployment status and rollout history.
     - `logs:read`: Tail or stream application logs in CI.
     - `resources:read`: Inspect Pod and Kubernetes runtime metadata.
   - Click **Generate API token** and **Copy token to clipboard**.
2. **REST API:**
   ```sh
   curl -X POST "$CRELAN_API_URL/api/v1/tokens" \
     -H "Cookie: crelan_session=<session-token>" \
     -H "Content-Type: application/json" \
     --data '{
       "name": "ci-deploy-token",
       "scopes": ["deployments:write", "deployments:read", "logs:read"],
       "expires_in_days": 90
     }'
   ```

Store `CRELAN_API_URL` and `CRELAN_TOKEN` as protected/masked secrets in your CI provider.

## Deployment flows

There are two primary deployment flows:

1. **Build in CI and deploy an image:** The pipeline checks out its own source, builds and pushes an image to a registry (Docker Hub, GHCR, GitLab Registry, ECR), then triggers rollout on Crelan. This works with GitLab CI, Jenkins, GitHub Actions, and any runner.
2. **Ask Crelan to build source:** Send a GitHub repository URL and branch. Crelan clones and builds it using rootless BuildKit (if a Dockerfile exists or via the built-in Dockerfile Generator) or Cloud Native Buildpacks (kpack). Private repositories require a connected GitHub App or a personal access token.

## CLI commands

The `crelan` CLI can be compiled with `go build -o crelan ./cmd/crelan` and installed in your runner:

```sh
# Deploy an existing container image
crelan deploy --name "$CRELAN_APP_NAME" \
  --image "$IMAGE_TAG" \
  --image-pull-secret "$CRELAN_IMAGE_PULL_SECRET" \
  --port 8080 --wait

# Deploy from a GitHub repository
crelan deploy --name "$CRELAN_APP_NAME" \
  --repo "$CRELAN_SOURCE_REPOSITORY" \
  --branch "${CRELAN_SOURCE_BRANCH:-main}" \
  --port 8080 --wait

# Check rollout status
crelan status "$CRELAN_APP_NAME"

# Inspect application logs
crelan logs "$CRELAN_APP_NAME"

# View rollout run history
crelan history "$CRELAN_APP_NAME"
```

## GitHub Actions

Use the official composite action at `.github/actions/deploy`:

```yaml
name: Deploy
on:
  push:
    branches: [ main ]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Deploy to Crelan
        uses: ./.github/actions/deploy
        with:
          api-url: ${{ vars.CRELAN_API_URL }}
          token: ${{ secrets.CRELAN_TOKEN }}
          app-name: storefront
          branch: main
          port: "8080"
```

## GitLab CI

Copy [gitlab-ci.yml](../examples/ci/gitlab-ci.yml) into the application repo as `.gitlab-ci.yml`. Configure `CRELAN_API_URL`, `CRELAN_TOKEN`, and `CRELAN_APP_NAME` as CI/CD variables; mark the token masked and protected. Set `CRELAN_IMAGE_PULL_SECRET` when deploying from a private registry. The image job uses GitLab's built-in registry and a Docker-in-Docker runner; the runner must permit privileged Docker services.

The `deploy:source` job is manual and demonstrates Crelan-managed source builds. Set `CRELAN_SOURCE_REPOSITORY` to a public GitHub URL and optionally `CRELAN_SOURCE_BRANCH`. For the GitLab project itself, use `deploy:image`.

## Jenkins

Copy [Jenkinsfile](../examples/ci/Jenkinsfile) into the application repo. Add Jenkins credentials with IDs `crelan-api-url`, `crelan-token`, and `container-registry`; update the credential IDs if needed. Set the `CRELAN_APP_NAME` and optional `CRELAN_IMAGE_PULL_SECRET` parameters. The pipeline builds and pushes the checked-out source by default. Set `CONTAINER_IMAGE_REPOSITORY` to the image repository and set the `Build in Crelan from a public GitHub repo` parameter to select the source-build example instead. The two source parameters configure that repository and branch.

For private registries, save a registry credential in your Crelan workspace and pass its name as `CRELAN_IMAGE_PULL_SECRET`. Public images do not need a pull secret.

