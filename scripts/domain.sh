#!/usr/bin/env bash
# Point a custom domain at the Cloud Run service, one action at a time.
#
# Only the middle of this is a command. Proving you own the domain happens in a
# browser and publishing records happens at a registrar, so each page is opened
# when its step arrives and nothing is listed before it is needed.
#
# Stop whenever you like and run it again; every step checks what is already done.
set -euo pipefail

: "${GOOGLE_CLOUD_PROJECT:?GOOGLE_CLOUD_PROJECT is required}"
: "${GOOGLE_CLOUD_REGION:?GOOGLE_CLOUD_REGION is required}"
: "${SERVICE_NAME:?SERVICE_NAME is required}"
: "${DOMAIN:?DOMAIN is required, for example: make domain DOMAIN=example.com}"

common=(--region "$GOOGLE_CLOUD_REGION" --project "$GOOGLE_CLOUD_PROJECT")
apex=${DOMAIN#www.}

step()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
info()  { printf '   %s\n' "$*"; }
ok()    { printf '   \033[32m✓\033[0m %s\n' "$*"; }
warn()  { printf '   \033[33m…\033[0m %s\n' "$*"; }

if { exec 3</dev/tty; } 2>/dev/null; then HAS_TTY=1
elif [[ -t 0 ]]; then exec 3<&0; HAS_TTY=1
else HAS_TTY=0; fi
interactive() { [[ "$HAS_TTY" == "1" ]]; }

# pause shows one instruction and blocks. Nothing else prints in between, so the
# terminal always holds exactly one thing to do.
pause() {
  interactive || { info "Nothing is attached to answer this. Run it again from a terminal."; exit 1; }
  printf '\n   \033[1m%s\033[0m ' "$1"
  read -r _ <&3 || exit 0
}

read_value() {
  interactive || return 1
  printf '\n   \033[1m%s\033[0m ' "$1"
  read -r "$2" <&3 || exit 0
}

copy() {
  if command -v pbcopy >/dev/null 2>&1; then printf '%s' "$1" | pbcopy && return 0
  elif command -v wl-copy >/dev/null 2>&1; then printf '%s' "$1" | wl-copy && return 0
  elif command -v xclip >/dev/null 2>&1; then printf '%s' "$1" | xclip -selection clipboard && return 0
  fi
  return 1
}

open_url() {
  [[ -n "$1" ]] || return 0
  info "$1"
  interactive || return 0
  if command -v open >/dev/null 2>&1; then open "$1" >/dev/null 2>&1 || true
  elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$1" >/dev/null 2>&1 || true
  fi
}

# Registrars move their URLs and none publish a contract for deep links; a stale
# one lands on the dashboard, which is where you would have started anyway.
dns_url() { printf '%s' "${DNS_CONSOLE_URL//\{domain\}/$apex}"; }

verified() {
  local candidate
  while read -r candidate; do
    [[ -z "$candidate" ]] && continue
    [[ "$DOMAIN" == "$candidate" || "$DOMAIN" == *".$candidate" ]] && return 0
  done < <(gcloud domains list-user-verified --format='value(id)' 2>/dev/null)
  return 1
}

mapped() { gcloud beta run domain-mappings describe --domain "$DOMAIN" "${common[@]}" >/dev/null 2>&1; }

records() {
  gcloud beta run domain-mappings describe --domain "$DOMAIN" "${common[@]}" --format=json 2>/dev/null |
    python3 -c '
import json, sys
for r in (json.load(sys.stdin).get("status") or {}).get("resourceRecords") or []:
    name = r.get("name") or "*"
    print("%s\t%s\t%s" % (r["type"], "@" if name == "*" else name, r["rrdata"]))
'
}

ready() {
  gcloud beta run domain-mappings describe --domain "$DOMAIN" "${common[@]}" --format=json 2>/dev/null |
    python3 -c '
import json, sys
for c in (json.load(sys.stdin).get("status") or {}).get("conditions") or []:
    if c.get("type") == "Ready":
        sys.exit(0 if c.get("status") == "True" else 1)
sys.exit(1)
'
}

# missing_records prints what DNS is still not serving, so a wait always says
# what it is waiting on rather than repeating the same prompt.
missing_records() {
  local type name value live
  while IFS=$'\t' read -r type name value; do
    live=$(dig +short "$type" "$DOMAIN" 2>/dev/null || true)
    grep -qxF "$value" <<<"$live" || printf '%s %s\n' "$type" "$value"
  done < <(records)
}

# ------------------------------------------------------- 1. prove ownership

step "Step 1 of 4 — prove you own $apex"

if verified; then
  ok "already verified"
else
  info "Search Console will say you do not have access to $apex. That is"
  info "expected. Click VERIFY YOUR OWNERSHIP, choose the DNS TXT method,"
  info "and copy the value it gives you."
  pause "Press enter to open Search Console."
  open_url "https://search.google.com/search-console/ownership?resource_id=sc-domain:$apex"

  TXT_VALUE=""
  read_value "Paste the TXT value here:" TXT_VALUE || true

  if [[ -n "$TXT_VALUE" ]]; then
    step "   Add it at your DNS host"
    info "Add a Custom Record:  TYPE TXT   NAME @   DATA <the value you pasted>"
    copy "$TXT_VALUE" && info "(the value is on your clipboard)"
    pause "Press enter to open your DNS settings."
    open_url "$(dns_url)"

    until dig +short TXT "$apex" 2>/dev/null | tr -d '"' | grep -qF "$TXT_VALUE"; do
      pause "Press enter once you have saved it."
      dig +short TXT "$apex" 2>/dev/null | tr -d '"' | grep -qF "$TXT_VALUE" || warn "not visible in DNS yet — it can take a few minutes"
    done
    ok "the TXT record is live"

    step "   Finish verification"
    info "Back in Search Console, click VERIFY."
  fi

  until verified; do
    pause "Press enter once Search Console says verified."
    verified || warn "Google still does not list $apex as verified"
  done
  ok "verified"
fi

# ------------------------------------------------------------- 2. the mapping

step "Step 2 of 4 — map $DOMAIN onto $SERVICE_NAME"

if mapped; then
  ok "already mapped"
else
  gcloud beta run domain-mappings create --service "$SERVICE_NAME" --domain "$DOMAIN" "${common[@]}" >/dev/null
  ok "created"
fi

# --------------------------------------------------------------- 3. the records

step "Step 3 of 4 — publish DNS records for $DOMAIN"

if [[ -z "$(missing_records)" ]]; then
  ok "records are already live"
else
  info "First delete any A, AAAA, www CNAME or HTTPS records your DNS host"
  info "added by default. They point at the host's own servers and would"
  info "fight the ones you are about to add. Leave mail records alone."
  pause "Press enter to open your DNS settings."
  open_url "$(dns_url)"
  pause "Press enter once those records are gone."

  total=$(records | wc -l | tr -d ' ')
  n=0
  while IFS=$'\t' read -r type name value; do
    n=$((n + 1))
    step "   Record $n of $total"
    info "TYPE $type   NAME $name   DATA $value"
    copy "$value" && info "(the value is on your clipboard)"
    pause "Add it under Custom records, then press enter."
  done < <(records)

  until [[ -z "$(missing_records)" ]]; do
    warn "still not visible in DNS:"
    missing_records | while read -r line; do info "  $line"; done
    pause "Press enter to check again."
  done
  ok "all records are live"
fi

# ----------------------------------------------------------- 4. the certificate

step "Step 4 of 4 — wait for the certificate"

until ready; do
  warn "not issued yet — this usually takes a few minutes"
  pause "Press enter to check again."
done

printf '\n   \033[1;32mhttps://%s is live\033[0m\n\n' "$DOMAIN"
