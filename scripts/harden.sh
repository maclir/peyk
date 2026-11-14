#!/usr/bin/env bash
# Closes what Firestore rules leave open, and watches spend. Safe to rerun.
#
# - Sign-up is disabled once the owner account exists, so a stranger's Google
#   sign-in fails at Firebase instead of creating an account the rules then
#   have to refuse. That needs Identity Platform, which this upgrades to.
# - The Firebase browser key only works from the app's own addresses.
# - A budget alerts at 50, 90 and 100 percent of BUDGET.
set -euo pipefail

: "${GOOGLE_CLOUD_PROJECT:?}"
: "${PEYK_ORIGIN:?PEYK_ORIGIN is required}"
: "${FIREBASE_AUTH_DOMAIN:?}"
: "${BILLING_ACCOUNT:?BILLING_ACCOUNT is required for the budget}"
BUDGET=${BUDGET:-5}

token=$(gcloud auth print-access-token)
api() {
  curl -sS --fail-with-body -H "Authorization: Bearer $token" -H "x-goog-user-project: $GOOGLE_CLOUD_PROJECT" \
    -H "Content-Type: application/json" "$@"
}
idt="https://identitytoolkit.googleapis.com"

users=$(api -X POST "$idt/v1/projects/$GOOGLE_CLOUD_PROJECT/accounts:query" -d '{"returnUserInfo":false}' |
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("recordsCount","0"))')
if [[ "$users" == "0" ]]; then
  echo "No accounts yet: sign in once on the phone and run make owner first, or nobody could sign up." >&2
  exit 1
fi

tier=$(api "$idt/admin/v2/projects/$GOOGLE_CLOUD_PROJECT/config" |
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("subtype",""))')
if [[ "$tier" != "IDENTITY_PLATFORM" ]]; then
  api -X POST "$idt/v2/projects/$GOOGLE_CLOUD_PROJECT/identityPlatform:initializeAuth" -d '{}' >/dev/null
  echo "Upgraded Firebase Auth to Identity Platform."
fi
api -X PATCH "$idt/admin/v2/projects/$GOOGLE_CLOUD_PROJECT/config?updateMask=client.permissions.disabledUserSignup" \
  -d '{"client":{"permissions":{"disabledUserSignup":true}}}' >/dev/null
echo "Sign-up disabled; $users existing account(s) still sign in."

key=$(gcloud services api-keys list --project "$GOOGLE_CLOUD_PROJECT" \
  --filter='displayName:"Browser key (auto created by Firebase)"' --format='value(uid)')
[[ -n "$key" ]] || { echo "Firebase browser key not found" >&2; exit 1; }
service_host=$(gcloud run services describe "${SERVICE_NAME:-peyk}" --project "$GOOGLE_CLOUD_PROJECT" \
  --region "${GOOGLE_CLOUD_REGION:-europe-west1}" --format='value(status.url)')
gcloud services api-keys update "$key" --project "$GOOGLE_CLOUD_PROJECT" --quiet \
  --allowed-referrers="$PEYK_ORIGIN/*,$service_host/*,https://$FIREBASE_AUTH_DOMAIN/*" >/dev/null 2>&1
echo "Browser key limited to $PEYK_ORIGIN, $service_host and https://$FIREBASE_AUTH_DOMAIN."

name="$GOOGLE_CLOUD_PROJECT budget"
if gcloud billing budgets list --billing-account="$BILLING_ACCOUNT" --format='value(displayName)' | grep -qxF "$name"; then
  echo "Budget '$name' exists."
else
  currency=$(gcloud billing accounts describe "$BILLING_ACCOUNT" --format='value(currencyCode)')
  gcloud billing budgets create --billing-account="$BILLING_ACCOUNT" --display-name="$name" \
    --budget-amount="${BUDGET}${currency}" --filter-projects="projects/$GOOGLE_CLOUD_PROJECT" \
    --threshold-rule=percent=0.5 --threshold-rule=percent=0.9 --threshold-rule=percent=1.0 >/dev/null
  echo "Budget '$name' of $BUDGET $currency created; billing admins get the alerts."
fi
