#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
source helm/versions.env

helm repo add jetstack https://charts.jetstack.io >/dev/null
helm repo add traefik https://traefik.github.io/charts >/dev/null
helm repo add kyverno https://kyverno.github.io/kyverno >/dev/null
helm repo add projectcapsule https://projectcapsule.github.io/charts >/dev/null
helm repo add projectcalico https://docs.tigera.io/calico/charts >/dev/null
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null
helm repo update >/dev/null

# deploy <release> <repo/chart> <namespace> <VERSION_VAR> <values-file> [extra helm args...]
deploy() {
  local release=$1 chart=$2 ns=$3 verkey=$4 values=$5
  shift 5
  local version="${!verkey:-}"
  if [ -z "$version" ]; then
    version=$(helm search repo --regexp "^${chart}\$" --output json | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["version"])')
    echo "::warning::${verkey} is not pinned. Using ${version}. Add ${verkey}=${version} to helm/versions.env"
  fi
  echo "=== ${release} (${chart} ${version}) ==="
  helm template "$release" "$chart" --version "$version" --namespace "$ns" -f "$values" "$@" >/dev/null
  helm upgrade --install "$release" "$chart" --version "$version" \
    --namespace "$ns" --create-namespace -f "$values" "$@" --wait --timeout 10m
}

# Order matters: network policy first, then cert-manager, ingress, policy engine, multi-tenancy, monitoring.
deploy calico        projectcalico/tigera-operator                tigera-operator CALICO_VERSION          helm/values/calico.yaml
deploy cert-manager  jetstack/cert-manager                        cert-manager    CERT_MANAGER_VERSION    helm/values/cert-manager.yaml
deploy traefik       traefik/traefik                              traefik         TRAEFIK_VERSION         helm/values/traefik.yaml
deploy kyverno       kyverno/kyverno                              kyverno         KYVERNO_VERSION         helm/values/kyverno.yaml
deploy capsule       projectcapsule/capsule                       capsule-system  CAPSULE_VERSION         helm/values/capsule.yaml

# Bootstrap resource: a Tenant custom resource, not a chart. Needs the Capsule CRDs and webhook to exist first.
kubectl apply -f manifests/capsule/dev-tenant.yaml

deploy capsule-proxy projectcapsule/capsule-proxy                 capsule-system  CAPSULE_PROXY_VERSION   helm/values/capsule-proxy.yaml

extra=()
if [ -n "${GRAFANA_ADMIN_PASSWORD:-}" ]; then
  extra+=(--set "grafana.adminPassword=${GRAFANA_ADMIN_PASSWORD}")
fi
deploy monitoring    prometheus-community/kube-prometheus-stack   monitoring      KUBE_PROMETHEUS_VERSION helm/values/monitoring.yaml "${extra[@]}"

if [ "${INSTALL_KATA:-false}" = "true" ]; then
  if [ -z "${KATA_DEPLOY_VERSION:-}" ]; then
    echo "::error::INSTALL_KATA=true requires KATA_DEPLOY_VERSION"; exit 1
  fi
  helm upgrade --install kata-deploy oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy \
    --version "$KATA_DEPLOY_VERSION" --namespace kube-system -f helm/values/kata-deploy.yaml --wait --timeout 15m
else
  echo "Skipping Kata (INSTALL_KATA is not true)."
fi