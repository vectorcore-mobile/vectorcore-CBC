# LTE cell inventory

The cell-inventory subsystem stores LTE cell, tracking-area (TAC), eNB, and
MME routing inventory in the CBC's existing SQLite database. It exists to
later map a CAP alert polygon to the E-UTRAN cells, TAIs, and MME peers a
warning must reach (TS 23.041's List of TAIs / Warning Area List). This
document covers the CSV interchange format, import/export/selection-preview
API, and the operator-facing decisions behind them.

SQLite is the authoritative store. CSV is only an operator interchange
format: importing loads validated rows into `lte_cells`; exporting produces
the same canonical CSV back out. The repository access is behind a
`InventoryRepository` / `SpatialMatcher` interface pair
(`internal/inventory`) specifically so a future PostgreSQL/PostGIS
implementation can replace the SQLite/pure-Go one without changing the API
or CAP-to-cell planning logic.

This is a preview/inventory feature only: nothing here transmits SBcAP or
otherwise touches live alert delivery.

## Enabling it

Cell inventory is off by default. Enable it in `cbc.yaml`:

```yaml
cell_inventory:
  enabled: true
  max_import_size_bytes: 10485760   # 10 MiB, the default
  default_import_mode: "validate-only"
```

It shares the CBC's `database.path` SQLite file; there is no separate
database to configure.

## CSV format

The canonical column order (also the exact header emitted by the export
endpoint):

```csv
mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,geometry_quality,source,source_record_id,source_version,active,coverage_geojson
```

Headers are matched case-insensitively with surrounding whitespace trimmed on
import, but every one of the 20 canonical headers must be present (this is
what keeps a round-trip export/re-import lossless) and **unknown headers are
rejected outright** — a misspelled column name fails the whole file rather
than being silently dropped.

| Column | Required | Notes |
| --- | --- | --- |
| `mcc` | yes | Exactly 3 digits; leading zeros preserved |
| `mnc` | yes | 2 or 3 digits; leading zeros preserved |
| `mnc_length` | yes | `2` or `3`, must match `mnc`'s digit count |
| `eci` | yes | 28-bit E-UTRAN Cell Identity, `0`-`268435455` |
| `enb_id` | yes | eNB ID for the macro eNB identity model, `0`-`1048575` |
| `local_cell_id` | yes | `0`-`255` |
| `tac` | yes | LTE Tracking Area Code, `0`-`65535` |
| `cell_name` | no | Free text |
| `mme_name` | no | Free text; used to group the selection-preview result |
| `latitude` | no | `-90`..`90` |
| `longitude` | no | `-180`..`180` |
| `nominal_radius_m` | no | Positive number |
| `azimuth_deg` | no | `0 <= value < 360` |
| `beamwidth_deg` | no | `0 < value <= 360` |
| `geometry_quality` | yes | One of the values below |
| `source` | no | Free text provenance |
| `source_record_id` | no | Free text provenance |
| `source_version` | no | Free text provenance |
| `active` | yes | `true`/`false` |
| `coverage_geojson` | no | GeoJSON `Polygon` or `MultiPolygon`, see below |

Any optional column may be left empty; an empty value is stored as absent
(`NULL`), not zero.

### Macro eNB identity model (the only one currently supported)

```
ECI == (eNB ID << 8) | local cell ID
```

This is the standard macro eNB encoding from **3GPP TS 23.003** (Numbering,
addressing and identification — archived at
[`docs/specs/23003-j30.zip`](specs/23003-j30.zip)), where the 28-bit E-UTRAN
Cell Identity splits into a 20-bit eNB ID and an 8-bit Cell Identity. Rows
that don't satisfy this invariant are rejected with `eci_identity_mismatch`.
Other eNB identity formats (e.g. home eNB / short macro variants) are **not**
validated or supported yet — do not import cells using a different identity
encoding without extending this check first.

### Geometry quality

One of:

```
engineered_polygon   propagation_model   sector_estimate
point_radius         site_point          unknown
```

`engineered_polygon` and `propagation_model` assert that the coverage area is
an actual surveyed/modeled polygon, so a row using either **must** also
supply `coverage_geojson` — the importer rejects
`geometry_quality_requires_polygon` otherwise. The other qualities describe
point/sector metadata and may have an empty `coverage_geojson`.

### `coverage_geojson`

Only GeoJSON `Polygon` and `MultiPolygon` are accepted. As with all GeoJSON,
**coordinates are `[longitude, latitude]`** — the opposite order from the
`latitude`/`longitude` CSV columns. Getting this backwards silently produces
a polygon on the wrong side of the planet, so double-check it when hand
authoring a row.

Because the value is JSON embedded in a CSV field, it must be CSV-quoted
with its own double quotes doubled up. For example, the field value

```json
{"type":"Polygon","coordinates":[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]}
```

must appear in the CSV row as:

```csv
"{""type"":""Polygon"",""coordinates"":[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]}"
```

Validation rejects: malformed JSON, any geometry type other than `Polygon`/
`MultiPolygon`, empty coordinate arrays, out-of-range coordinates, and rings
with fewer than 3 distinct vertices. It does **not** attempt to repair
heavily malformed polygons. The one normalization it does perform — and
which is covered by tests — is auto-closing a ring whose last position
doesn't repeat the first, provided the ring otherwise has at least 3
vertices. The stored/exported GeoJSON is this normalized form, so a
re-imported export may differ byte-for-byte from a hand-authored input while
remaining the same shape.

## Import modes

```
validate-only   merge   replace
```

* **validate-only** — parses and validates the whole file, computes hashes,
  bounding boxes, and normalized geometry, and records an import audit row —
  but never touches `lte_cells` and never creates an inventory version. Use
  it to check a file before committing to it.
* **merge** — inserts new cells and updates existing ones (matched on
  `mcc`/`mnc`/`mnc_length`/`eci`); cells present in the database but absent
  from the file are left untouched.
* **replace** — treats the file as the complete active inventory: inserts
  new cells, updates existing ones, and marks cells absent from the file
  `active=false`. It never physically deletes a row.

For both `merge` and `replace`, the entire file is parsed and validated
first; if any row is invalid, nothing is applied — the live inventory is
guaranteed to be all-or-nothing per import. Applying validated rows,
creating the resulting inventory version, and marking the import complete
all happen in one SQLite transaction, so a failure partway through leaves the
previous inventory state exactly as it was.

The import endpoint always returns HTTP `200` with a JSON summary, even when
rows were rejected — check `status`/`rowsRejected` in the response (or fetch
`.../errors`) rather than treating a 200 as unconditional success. Real HTTP
error statuses (`400`, `413`) are reserved for structural problems: an
invalid `mode`, a missing `file` field, or an oversized upload.

## Endpoints

All endpoints below require the same bearer token as the rest of the
operator API.

### Import

```
POST /v1/cell-inventory/imports?mode=validate-only|merge|replace
Content-Type: multipart/form-data; field name "file"
```

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  -F "file=@docs/example-lte-cell-inventory.csv" \
  "http://localhost:8087/v1/cell-inventory/imports?mode=validate-only"
```

```json
{
  "importId": "imp-...",
  "mode": "validate-only",
  "status": "validated",
  "sourceFilename": "example-lte-cell-inventory.csv",
  "sourceSha256": "...",
  "rowsReceived": 3,
  "rowsValid": 3,
  "rowsRejected": 0,
  "inserted": 0,
  "updated": 0,
  "deactivated": 0,
  "warnings": 0
}
```

Re-run with `?mode=merge` to actually load the example inventory.

### Get import result / errors

```
GET /v1/cell-inventory/imports/{importID}
GET /v1/cell-inventory/imports/{importID}/errors
```

Each error identifies `row` (1-indexed including the header row), `column`,
`code`, and `message`.

### List / get cells

```
GET /v1/cell-inventory/cells?active=true&mcc=311&mnc=435&tac=1&mmeName=MME1&enbId=4096&eci=1048577&limit=50&offset=0
GET /v1/cell-inventory/cells/{cellID}
```

All filters are optional and combine with AND; `limit`/`offset` bound
pagination (default limit 50, max 500).

### Export

```
GET /v1/cell-inventory/export?format=csv&active=true
```

Returns `text/csv` with `Content-Disposition: attachment`, plus
`X-Inventory-Version`, `X-Exported-At`, and `X-Record-Count` headers. The
output uses the same canonical column order the importer accepts, so
`export | validate-only re-import` always succeeds without data loss.

### Selection preview

```
POST /v1/cell-inventory/selection-preview
Content-Type: application/json
```

```json
{
  "plmn": {"mcc": "311", "mnc": "435", "mncLength": 3},
  "policy": "conservative-intersection",
  "area": {
    "type": "Polygon",
    "coordinates": [[[-86.3100,32.3700],[-86.2800,32.3900],[-86.2500,32.3600],[-86.3100,32.3700]]]
  }
}
```

Lookup flow: validate the request geometry, compute its bounding box, ask
SQLite for cells whose stored bounding box overlaps it (or, for cells with no
polygon, whose center point falls within it), then run precise Go geometry
comparison against each candidate.

The only supported policy today is `conservative-intersection`: a cell is
selected when its stored coverage polygon intersects the requested area
(reason `coverage_intersection`), or — **only when it has no coverage
geometry at all** — when its center point lies inside the requested area
(reason `center_inside`). Results are grouped by `mmeName` into TAIs
(deduplicated `mcc`/`mnc`/`tac`) and ECIs (deduplicated).

This endpoint is a preview only: it never sends SBcAP or touches alert
delivery.

## Trying it against the example file

```sh
docs/example-lte-cell-inventory.csv
```

contains three fictional lab cells on PLMN `311/435`, two TACs, two eNB IDs,
one explicit GeoJSON polygon (`engineered_polygon`), one point/radius cell,
and one azimuth/beamwidth sector estimate, with `MME1`/`MME2` associations.

```sh
# 1. validate
curl -sS -H "Authorization: Bearer $TOKEN" -F "file=@docs/example-lte-cell-inventory.csv" \
  "http://localhost:8087/v1/cell-inventory/imports?mode=validate-only"

# 2. load it
curl -sS -H "Authorization: Bearer $TOKEN" -F "file=@docs/example-lte-cell-inventory.csv" \
  "http://localhost:8087/v1/cell-inventory/imports?mode=merge"

# 3. export and confirm it round-trips
curl -sS -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8087/v1/cell-inventory/export?format=csv" -o roundtrip.csv
curl -sS -H "Authorization: Bearer $TOKEN" -F "file=@roundtrip.csv" \
  "http://localhost:8087/v1/cell-inventory/imports?mode=validate-only"
```
