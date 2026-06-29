#!/usr/bin/env bash
# provision.sh — provision a destination Grafana stack with everything synthkit's customer
# dashboards need: a NORMAL Infinity datasource pointing at synthkit's control plane, plus the
# generated customer dashboard resources. Staff-run; uses gcx with a named context. Secrets
# (CONTROL_TOKEN) are read from the environment, never committed.
#
# Connectivity model: synthkit serves its control plane over plain HTTP on :8088. The operator
# fronts it with `tailscale serve` so it gets a browser-trusted real cert at a tailnet address
# (e.g. https://your-host.example.com). The Infinity datasource is a normal proxy datasource
# pointed at that tailnet URL — no self-signed cert, no cert pinning, no secure-socks proxy here.
#
# The USER configures Grafana Cloud's PDC / Tailscale connection (adding a tailnet auth key) so
# Grafana Cloud can reach this url privately. That is done in the Grafana Cloud UI, not by this
# script.
#
# Usage:
#   provisioning/provision.sh --context <customer-stack> [--base-url https://your-host.example.com]
# Basic auth: export CONTROL_TOKEN=… first (matches synthkit's CONTROL_TOKEN). Unset = open.
set -euo pipefail

CONTEXT="" BASE_URL="" DS_NAME="synthkit (Infinity)" DASHBOARDS="dashboards/customer"
while [ $# -gt 0 ]; do
  case "$1" in
    --context) CONTEXT="$2"; shift 2;;
    --base-url) BASE_URL="$2"; shift 2;;
    --ds-name) DS_NAME="$2"; shift 2;;
    --dashboards) DASHBOARDS="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[ -n "$CONTEXT" ] || { echo "required: --context" >&2; exit 2; }

gcx_api() { gcx --context "$CONTEXT" api "$@"; }

# 1. Build the Infinity datasource payload (a normal proxy datasource + optional HTTP Basic auth).
PAYLOAD="$(BASE_URL="$BASE_URL" DS_NAME="$DS_NAME" CONTROL_TOKEN="${CONTROL_TOKEN:-}" python3 - <<'PY'
import json, os
url, name, token = os.environ["BASE_URL"], os.environ["DS_NAME"], os.environ.get("CONTROL_TOKEN", "")
ds = {"name": name, "type": "yesoreyeram-infinity-datasource", "access": "proxy", "url": url}
secure = {}
if token:                            # basic auth only when CONTROL_TOKEN is set (matches synthkit)
    ds.update(basicAuth=True, basicAuthUser="control")
    secure["basicAuthPassword"] = token
else:
    ds["basicAuth"] = False
if secure:
    ds["secureJsonData"] = secure
print(json.dumps(ds))
PY
)"

# 2. Create or update the datasource (idempotent: PUT if the name already exists, else POST).
echo "→ provisioning datasource '$DS_NAME' on $CONTEXT"
DS_NAME_ENC="$(DS_NAME="$DS_NAME" python3 -c 'import urllib.parse,os;print(urllib.parse.quote(os.environ["DS_NAME"],safe=""))')"
# NOTE: not named UID — that variable is readonly in bash.
DS_UID="$(gcx_api "/api/datasources/name/$DS_NAME_ENC" -o json 2>/dev/null | python3 -c 'import sys,json
try: print(json.load(sys.stdin).get("uid",""))
except Exception: print("")' || true)"
if [ -n "$DS_UID" ]; then
  echo "  updating existing datasource uid=$DS_UID"
  printf '%s' "$PAYLOAD" | gcx_api "/api/datasources/uid/$DS_UID" -X PUT -d @- >/dev/null
else
  echo "  creating datasource"
  printf '%s' "$PAYLOAD" | gcx_api /api/datasources -d @- >/dev/null
fi

# 3. Push the generated customer dashboard resources (GA v2 manifests) to the same stack.
echo "→ pushing dashboards under $DASHBOARDS"
gcx --context "$CONTEXT" resources push -p "$DASHBOARDS"

echo "✓ provisioned datasource + dashboards on $CONTEXT"
echo "  Reminder: enable Grafana Cloud's PDC Tailscale connection (add a tailnet auth key) so"
echo "  Grafana Cloud can reach $BASE_URL privately."
