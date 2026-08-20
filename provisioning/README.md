# provisioning — set up a destination stack for synthkit's customer surfaces

Staff-run provisioning of a destination Grafana stack with everything the customer dashboards
need. Two things get created:

1. **The Infinity datasource** (`synthkit (Infinity)`) — backs the customer control dashboard's
   reads/writes. It is a **normal proxy datasource** pointed at synthkit's control plane at a
   **tailnet address** (default `https://your-host.example.com`). No self-signed cert, no cert
   pinning, no secure-socks proxy.
2. **The customer dashboard resources** — the generated GA v2 dashboards under
   [`../dashboards/customer/`](../dashboards/customer) (blueprint dashboards via `cmd/synthkit-dash`
   + the control dashboard via `cmd/synthkit-control-dash`).

## Connectivity model

synthkit serves its control plane over **plain HTTP on :8088**. The operator fronts it with
**`tailscale serve`**, which terminates a **browser-trusted real cert** at the host's tailnet
address — so the dashboard's write buttons (browser → synthkit) work with no cert warning.

The **user** then enables **Grafana Cloud's PDC Tailscale connection** (adding a **tailnet auth
key**) so Grafana Cloud can reach that tailnet URL **privately** to run the datasource's read
queries. There is **no pdc-agent**, no Tailscale Funnel, and no self-signed cert involved.

## Usage

```bash
provisioning/provision.sh --context <customer-stack> \
  --base-url https://<host>.<tailnet>.ts.net

# Authenticated control plane: export the SAME secret synthkit uses (HTTP Basic, user "control"):
export CONTROL_TOKEN=…           # never committed; matches synthkit's .env CONTROL_TOKEN
provisioning/provision.sh --context <customer-stack> --base-url https://<host>.<tailnet>.ts.net
```

The script is idempotent: it PUTs an existing `synthkit (Infinity)` datasource or POSTs a new one,
then `gcx resources push`es the dashboards.

## Auth model (recap)

- **GET** routes used by Infinity require HTTP Basic. The provisioner requires `CONTROL_TOKEN` and
  stores it only in datasource `secureJsonData`.
- **POST** routes used by dashboard action buttons are browser-direct, so datasource credentials do
  not apply. The browser performs a separate Basic challenge against the same HTTPS origin.
- Transport trust comes from `tailscale serve`'s browser-trusted cert; the datasource is a normal
  proxy datasource (no pinned cert, no skip-verify).

See [`../dashboards/customer/README.md`](../dashboards/customer/README.md) for the dashboards and
[`../dashboards/CLAUDE.md`](../dashboards/CLAUDE.md) for push/validate/snapshot discipline.
