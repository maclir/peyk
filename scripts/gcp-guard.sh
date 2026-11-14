#!/usr/bin/env bash
# Refuse to touch Google Cloud unless the identity and project are explicit.
#
# A personal project shares a machine with work ones, and `gcloud` keeps a
# single globally active account and project. So every cloud command in this
# repository is pinned through CLOUDSDK_CORE_ACCOUNT and CLOUDSDK_CORE_PROJECT
# (exported by the Makefile) rather than through that global configuration.
# Nothing here ever runs `gcloud config set`: whatever you had selected before
# a deploy is still selected afterwards.
set -euo pipefail

mode=${1:-deploy}

fail() {
  echo "$*" >&2
  exit 1
}

case "$mode" in
deploy)
  [[ -n "${GOOGLE_CLOUD_PROJECT:-}" ]] ||
    fail "GOOGLE_CLOUD_PROJECT is required. Set it in .env, or pass it on the command line."
  [[ -n "${GCLOUD_ACCOUNT:-}" ]] ||
    fail "GCLOUD_ACCOUNT is required so this cannot run as the wrong identity.
Set it in .env, for example: GCLOUD_ACCOUNT=you@example.com"

  gcloud auth list --filter="account=$GCLOUD_ACCOUNT" --format='value(account)' 2>/dev/null |
    grep -qxF "$GCLOUD_ACCOUNT" ||
    fail "Not authenticated as $GCLOUD_ACCOUNT.
Run: gcloud auth login $GCLOUD_ACCOUNT"

  # Read the global setting with the pin removed, so the reassurance below is
  # about the configuration on disk rather than about our own environment.
  active=$(env -u CLOUDSDK_CORE_PROJECT gcloud config get-value project 2>/dev/null || true)
  if [[ -n "$active" && "$active" != "$GOOGLE_CLOUD_PROJECT" ]]; then
    echo "    your active gcloud project is '$active'; this run is pinned elsewhere and leaves it alone"
  fi

  echo "==> $GCLOUD_ACCOUNT @ $GOOGLE_CLOUD_PROJECT"
  ;;

adc)
  # Application Default Credentials are what a local process uses to reach
  # Google APIs. The quota project is pinned per-process by the Makefile rather
  # than stored globally, so only the credentials themselves are checked here.
  [[ -n "${QUOTA_PROJECT:-}" ]] ||
    fail "QUOTA_PROJECT is required. Set it in .env, or pass it on the command line."

  adc="${CLOUDSDK_CONFIG:-$HOME/.config/gcloud}/application_default_credentials.json"
  [[ -f "$adc" ]] ||
    fail "No Application Default Credentials found.
Run: gcloud auth application-default login"

  stored=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("quota_project_id",""))' "$adc")
  if [[ -n "$stored" && "$stored" != "$QUOTA_PROJECT" ]]; then
    echo "    ADC stores quota project '$stored'; this run bills $QUOTA_PROJECT and leaves that alone"
  fi

  echo "==> local credentials billing $QUOTA_PROJECT"
  ;;

*)
  fail "Unknown guard mode: $mode"
  ;;
esac
