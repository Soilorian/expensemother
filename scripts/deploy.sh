#!/usr/bin/env bash
set -euo pipefail

# Runs on a Linux Docker host. Credentials are provisioned there, never in the image.
deploy_path=${1:?Usage: deploy.sh /absolute/app/path ghcr.io/owner/repo@sha256:digest compose-file}
image=${2:?Image digest required}
compose_source=${3:?Production Compose file required}
[[ "$deploy_path" =~ ^/[a-zA-Z0-9_/-]+$ && "$deploy_path" != / ]]
[[ "$image" =~ ^ghcr.io/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$ ]]
test -f "$compose_source"
compose_source=$(realpath -- "$compose_source")
test -f "$deploy_path/.env" || { echo 'Provision the server .env before deploying.' >&2; exit 1; }
cd "$deploy_path"
umask 077
exec 9>.deployment.lock
flock -w 600 9
export APP_IMAGE="$image"
engine=(docker)
if ! docker info >/dev/null 2>&1; then engine=(sudo -n docker); fi
registry_config=
cleanup() {
  if [[ -n "$registry_config" ]]; then rm -rf "$registry_config"; fi
  unset REGISTRY_TOKEN
}
trap cleanup EXIT
if [[ -n "${REGISTRY_TOKEN:-}" ]]; then
  registry_config=$(mktemp -d)
  printf '%s' "$REGISTRY_TOKEN" | docker --config "$registry_config" login ghcr.io --username "${4:?Registry username required}" --password-stdin
  unset REGISTRY_TOKEN
  engine+=(--config "$registry_config")
fi
compose=("${engine[@]}" compose --project-name expensemother --env-file .env -f "$compose_source")
# Validate and pull with the incoming configuration before touching the current deployment.
"${compose[@]}" config --quiet
"${compose[@]}" pull
# Stop the old poller before starting its replacement. Keep all persisted volumes.
"${compose[@]}" stop app
if ! "${compose[@]}" up -d --wait --wait-timeout 180; then
  echo 'Deployment failed. Inspect Compose status; previous image/config are retained for manual rollback.' >&2
  "${compose[@]}" ps
  exit 1
fi
# Only advance the saved release after readiness succeeds, including on retries.
if [[ -f .release.env ]]; then cp .release.env .previous-release.env; fi
if [[ -f compose.production.yaml ]]; then cp compose.production.yaml compose.previous.yaml; fi
if [[ "$compose_source" != "$PWD/compose.production.yaml" ]]; then
  cp "$compose_source" compose.production.yaml
fi
printf 'APP_IMAGE=%s\n' "$image" > .release.env
"${compose[@]}" ps
