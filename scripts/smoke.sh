#!/usr/bin/env bash
set -euo pipefail

: "${GOOGLE_CLOUD_PROJECT:?GOOGLE_CLOUD_PROJECT is required}"
GOOGLE_CLOUD_REGION=${GOOGLE_CLOUD_REGION:-europe-west1}
SERVICE_NAME=${SERVICE_NAME:-peyk}

url=$(gcloud run services describe "$SERVICE_NAME" \
  --project "$GOOGLE_CLOUD_PROJECT" \
  --region "$GOOGLE_CLOUD_REGION" \
  --format='value(status.url)')

status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$url/health")
if [[ "$status" != "204" ]]; then
  echo "Smoke test failed: $url/health returned $status, want 204" >&2
  exit 1
fi
echo "Smoke test passed: $url"

