# VectorCore CBC

VectorCore CBC receives CAP 1.2 alerts from its Cell Broadcast Entity (CBE) via
XEP-0127/XMPP, validates and deduplicates them, and exposes
the current alert state through a small operator API.

The radio-facing encoder emits LTE SBcAP APER (TS 29.168) using a hand-written,
pure-Go codec (`internal/sbcap`) - no CGO, no external ASN.1/PER library -
including PLMN-, TA-, and cell-wide Warning-Area-Lists.

## Run

```sh
cp config/cbc.yaml.example config/cbc.yaml
make clean
make 
./bin/cbc -c config/cbc.yaml
```

The operator API (`GET /v1/alerts`, `GET /v1/alerts/{identifier}`, `GET
/v1/audit`, `GET /healthz`, `GET /readyz`, `GET /metrics`) has no
authentication for now. Alerts and their lifecycle audit events persist in
SQLite. `GET /v1/alerts/{identifier}/cbs` returns the prepared TS 23.041 CBS
plan. When `sbcap.enabled` is set, the daemon maintains SCTP associations to
configured MME peers.

A web UI is served at `/ui/` (built React app, embedded into the binary via
`go:embed` - see "Web UI" below), showing the dashboard, alert list/detail,
and cell inventory.

When `cell_inventory.enabled` is set, the CBC also stores an LTE cell/TAC/eNB/
MME inventory in SQLite, importable and exportable as CSV, with a polygon
selection-preview endpoint for later CAP-to-cell mapping — see
[cell-inventory.md](docs/cell-inventory.md).

