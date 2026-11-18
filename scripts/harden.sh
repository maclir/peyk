#!/usr/bin/env bash
# Closes what Firestore rules leave open, and watches spend. Safe to rerun.
#
# - Sign-up is disabled once the owner account exists, so a stranger's Google
#   sign-in fails at Firebase instead of creating an account the rules then
#   have to refuse. That needs Identity Platform, which this upgrades to.
# - Google sign-in and the Firebase browser key work only from the app's own
#   addresses.
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

# Sign-in refuses a page whose host is not authorized, so a new PEYK_ORIGIN
# must be added here before the phone can sign in on it.
origin_host=${PEYK_ORIGIN#https://}
domains=$(api "$idt/admin/v2/projects/$GOOGLE_CLOUD_PROJECT/config" |
  python3 -c 'import json,sys; d=json.load(sys.stdin).get("authorizedDomains",[]); h=sys.argv[1]; print(json.dumps({"authorizedDomains": d if h in d else d+[h]}))' "$origin_host")
api -X PATCH "$idt/admin/v2/projects/$GOOGLE_CLOUD_PROJECT/config?updateMask=authorizedDomains" -d "$domains" >/dev/null
echo "Sign-in authorized on $origin_host."

key=$(gcloud services api-keys list --project "$GOOGLE_CLOUD_PROJECT" \
  --filter='displayName:"Browser key (auto created by Firebase)"' --format='value(uid)')
[[ -n "$key" ]] || { echo "Firebase browser key not found" >&2; exit 1; }
# status.url names only one of the service's run.app addresses; an installed
# app on the other would lose sign-in, so every address in the annotation stays.
service_urls=$(gcloud run services describe "${SERVICE_NAME:-peyk}" --project "$GOOGLE_CLOUD_PROJECT" \
  --region "${GOOGLE_CLOUD_REGION:-europe-west1}" --format=json |
  python3 -c 'import json,sys; print(",".join(u+"/*" for u in json.loads(json.load(sys.stdin)["metadata"]["annotations"]["run.googleapis.com/urls"])))')
gcloud services api-keys update "$key" --project "$GOOGLE_CLOUD_PROJECT" --quiet \
  --allowed-referrers="$PEYK_ORIGIN/*,$service_urls,https://$FIREBASE_AUTH_DOMAIN/*" >/dev/null 2>&1
echo "Browser key limited to $PEYK_ORIGIN/*,$service_urls,https://$FIREBASE_AUTH_DOMAIN/*."

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
