#!/usr/bin/env bash
set -euo pipefail
: "${APP_IMAGE:?Set APP_IMAGE to the locally built image}"
export POSTGRES_PASSWORD=ci-compose-password
export AUTH_SECRET=ci-only-secret-at-least-thirty-two-bytes
export TELEGRAM_BOT_TOKEN='' TELEGRAM_CLIENT_ID='' ALLOWED_ORIGINS='' WEB_APP_URL=''
export HTTP_PORT=17008
compose=(docker compose --project-name expensemother-ci --env-file /dev/null -f compose.production.yaml)
cleanup() {
  result=$?
  if (( result != 0 )); then "${compose[@]}" logs --no-color --tail=100; fi
  # Only this disposable CI project has its volumes removed.
  "${compose[@]}" down --volumes
  exit "$result"
}
trap cleanup EXIT
"${compose[@]}" config --quiet
"${compose[@]}" up -d --wait --wait-timeout 180
curl --fail --silent http://127.0.0.1:17008/healthz
curl --fail --silent http://127.0.0.1:17008/readyz
"${compose[@]}" exec -T app sh -c 'tesseract --list-langs 2>/dev/null | grep -qx fas && tesseract --list-langs 2>/dev/null | grep -qx eng'
python3 scripts/ci-api-smoke.py
