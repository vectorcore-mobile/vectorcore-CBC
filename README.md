# VectorCore CBC

VectorCore CBC receives CAP 1.2 alerts from its Cell Broadcast Entity (CBE) via
XEP-0127/XMPP, validates and deduplicates them, and exposes
the current alert state through a small operator API.

The radio-facing encoder emits LTE SBcAP APER (TS 29.168) using a hand-written,
pure-Go codec (`internal/sbcap`) - no CGO, no external ASN.1/PER library -
including PLMN-, TA-, and cell-wide Warning-Area-Lists.

## Architecture

```
CBE -- CAP 1.2 / XEP-0127 over XMPP --> CBC ingest
                                                    |
                                         validation + deduplication
                                                    |
                                          Publisher interface
                                                    |
                                               SBcAP (MME)
```

The CBE is configured as `cbe`, not merely as an arbitrary XMPP broker. The
client implements the documented receive-only flow: stream open, optional
STARTTLS, SASL PLAIN, stream restart, resource binding, and CAP `<message>`
reception.

CAP identifiers are the idempotency key. `Alert` and `Update` replace the
stored state for their identifier; references on updates are marked
superseded. The CBE may not forward `Cancel` messages, but the domain service
handles one defensively if a different CBE supplies it.

Alert state and lifecycle audit records are stored in SQLite. On startup the
CBC expires alerts whose CAP `expires` value has elapsed, then restores the
remaining state before reconnecting to the CBE. Operator endpoints require a
bearer token; health and readiness probes remain unauthenticated.

CAP messages are prepared into durable CBS plans before any RAN delivery is
attempted. A plan contains a TS 23.041 message identifier, serial number (GS,
message code, update number), DCS, page parameter, and fixed 82-octet pages.
The mapper uses GSM 7-bit when possible and falls back to UCS-2. CAP geocodes
named `cell`, `cgi`, `ecgi`, `nci`, or `nr-cgi` produce cell-wide targeting;
`tac`, `tai`, or `tracking_area` produce tracking-area-wide targeting.
Untargeted CAP alerts are rejected unless `cbs.allow_plmn_wide` is explicitly
enabled. CAP `<area>` blocks also support multiple `<polygon>` elements and
CAP 1.2's `<circle>` (`"lat,lon radius"`, km) — see "Cell Inventory" below —
all unioned with any `<geocode>`-derived cells when targeting a CBS message.
Mixing cell-scoped and tracking-area-scoped targets in one alert is rejected:
TS 29.168's Write-Replace Warning Request is scoped to either a Cell List or
a Tracking-Area List, never both, at the protocol level.

TS 23.041 determines the CBS message mapping, serial-number/message-identifier
rules, and geographic scope. TS 29.168 defines the CBC/MME SBcAP transport.
Implementing its ASN.1 APER wire format is intentionally deferred: the
radio-facing publisher must have conformance vectors and MME peer integration
tests before it is enabled for live broadcast delivery. (The underlying spec
documents themselves are archived under `docs/specs/` for anyone reading the
Go code alongside them: TS 23.041, TS 29.168, and TS 23.003 for cell-identity
encoding.)

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

## Cell Inventory

When `cell_inventory.enabled` is set, the CBC stores an LTE cell/TAC/eNB/MME
inventory in its existing SQLite database (`cell_inventory.max_import_size_bytes`,
default 10 MiB, and `cell_inventory.default_import_mode` are also
configurable). It exists to map a CAP alert polygon/circle to the E-UTRAN
cells, TAIs, and MME peers a warning must reach.

Cells can be bulk imported/exported as CSV, or created/deleted one at a time
via the API/UI (`POST`/`DELETE /v1/cell-inventory/cells`; delete is blocked
with `409` rather than cascading if the cell still has Geo Code mappings —
remove those first). The canonical CSV column order (also what the export
endpoint emits) is:

```csv
mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,geometry_quality,source,source_record_id,source_version,active,coverage_geojson
```

Headers are matched case-insensitively, all 20 must be present, and unknown
headers are rejected outright. `eci` must satisfy the macro eNB identity model
from TS 23.003: `eci == (enb_id << 8) | local_cell_id`. `geometry_quality` is
one of `engineered_polygon`, `propagation_model`, `sector_estimate`,
`point_radius`, `site_point`, `unknown` — the first two require a
`coverage_geojson` value. `coverage_geojson` accepts GeoJSON `Polygon`/
`MultiPolygon` only, with coordinates in `[longitude, latitude]` order (the
opposite of the `latitude`/`longitude` CSV columns).

Import modes are `validate-only` (parse/validate only, nothing applied),
`merge` (upsert; rows absent from the file are left untouched), and `replace`
(rows absent from the file are marked `active=false`, never physically
deleted). Both `merge` and `replace` are all-or-nothing: if any row is
invalid, nothing is applied.

`POST /v1/cell-inventory/selection-preview` takes a PLMN and a GeoJSON
`Polygon`/`MultiPolygon` area and returns the cells that intersect it, grouped
by `mmeName` into TAIs and ECIs. The only policy today is
`conservative-intersection`: a cell is selected when its stored coverage
polygon intersects the area, or — only when it has no coverage geometry at
all — when its center point lies inside the area. This endpoint (and the
whole cell-inventory feature) is preview/planning only: nothing here
transmits SBcAP or touches live alert delivery.

## Geo Codes

A Geo Codes registry (`/v1/geocode-registry`) lets an operator define
arbitrary CAP `<geocode valueName="...">` types — any scheme string is
accepted, not just a fixed set. `SAME` and `UGC` (the codes NWS/WEA alerts
commonly carry) are the usual starting point; a registry entry is just
`(type, code, description)`, e.g.:

| Type | Code | Description |
| --- | --- | --- |
| SAME | 001101 | Autauga County, AL |
| UGC | ALZ057 | Autauga, AL |

Each registry entry is then mapped to one or more of the operator's own cells
(`/v1/geocodes`, the "Cell Mappings" table) — a hand-curated membership
mapping, not a boundary polygon, so an inbound alert carrying only a
`<geocode>` (no polygon) can still be targeted to real cells. The mapping CSV
format is:

```csv
mcc,mnc,mnc_length,eci,code_type,code
311,435,3,1048577,SAME,001101
311,435,3,1048577,UGC,ALZ057
```

`POST /v1/geocodes/resolve` (`{"codeType": "SAME", "code": "001101"}`) tests
what a given `(type, code)` resolves to — the same lookup live alert
targeting uses via `namedGeocodeCells`. An unmatched geocode type/code
contributes zero cells rather than erroring, so one unrecognized `<geocode>`
in an alert never blocks the rest of that alert's targeting. The web UI's
**Geo Codes** page covers both the registry and the cell-mapping table.

## Web UI

`web/dist` (the built UI) is committed to git, so `make build` / `go build`
work with only the Go toolchain - Node is only needed to modify the UI:

```sh
make ui       # cd web && npm install && npm run build
make dev-ui   # Vite dev server, proxies /v1, /healthz, /readyz, /metrics to :8087
```

### Drawing coverage areas on the map

The **Cell Inventory** page uses a shared Leaflet + `leaflet-draw` map popup
in two places:

- **Add Cell** → "Draw Coverage Area" — draw a polygon, rectangle, or circle
  for that cell's `coverage_geojson`. Omnidirectional antennas should use a
  circle rather than leaving coverage empty, since the spatial matcher only
  falls back to radius-aware behavior when real coverage geometry is present
  (a center-point-only cell just matches on its point). Clicking anywhere on
  the map outside the draw tools also fills in that cell's Latitude/Longitude.
- **Selection Preview** → "Draw Area" — draw the CAP-alert-style area
  (polygon/rectangle/circle) to preview which cells/TAIs/MMEs it would
  select, without sending anything.

Drawn circles are approximated as polygons client-side, the same way CAP
`<circle>` alerts are approximated server-side — both are plain
degree-per-km math, not geodesic-accurate, consistent with the rest of the
inventory's precision level.

## Release gate

Run this gate from a clean build host (pure Go toolchain only; no CGO, no
external C libraries) before a release:

```sh
make verify VERSION=0.1.0
```

The gate runs unit and simulated-MME integration tests, the Go race detector,
`go vet`, a versioned production build, and strict YAML configuration
parsing.

Before deployment, an operator must also verify the following in its
controlled network:

- The CBE certificate chain and XMPP credentials are loaded from the secret
  store; `insecure_skip_verify` remains false.
- Each MME SCTP address is reachable and has passed Write-Replace and
  Stop-Warning acknowledgement tests for PLMN, TA, and cell targets.
- The configured message identifiers, repetition period, and broadcast count
  are approved for the operator's public-warning policy.
- `/healthz`, `/readyz`, and `/metrics` are monitored, and the SQLite data
  directory is backed up with access restricted to the service account.
- The systemd unit is installed with `/var/lib/vectorcore-cbc` as its only
  writable persistent path.
