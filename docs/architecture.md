# CBC architecture

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
client implements the documented receive-only flow: stream open,
optional STARTTLS, SASL PLAIN, stream restart, resource binding, and CAP
`<message>` reception.

CAP identifiers are the idempotency key. `Alert` and `Update` replace the
stored state for their identifier; references on updates are marked
superseded. The CBE may not forward `Cancel` messages, but the domain
service handles one defensively if a different CBE supplies it.

Alert state and lifecycle audit records are stored in SQLite. On startup the
CBC expires alerts whose CAP `expires` value has elapsed, then restores the
remaining state before reconnecting to the CBE. Operator endpoints require a
bearer token; health and readiness probes remain unauthenticated.

CAP messages are prepared into durable CBS plans before any RAN delivery is
attempted. A plan contains TS 23.041 message identifier, serial number (GS,
message code, update number), DCS, page parameter and fixed 82-octet pages.
The mapper uses GSM 7-bit when possible and falls back to UCS-2. CAP geocodes
named `cell`, `cgi`, `ecgi`, `nci` or `nr-cgi` produce cell-wide targeting;
`tac`, `tai` or `tracking_area` produce tracking-area-wide targeting. Untargeted
CAP alerts are rejected unless `cbs.allow_plmn_wide` is explicitly enabled.

TS 23.041 determines the CBS message mapping, serial-number/message-identifier
rules and geographic scope. TS 29.168 defines the CBC/MME SBcAP transport.
Implementing its ASN.1 APER wire format is intentionally deferred: the
radio-facing publisher must have conformance vectors and MME peer integration
tests before it is enabled for live broadcast delivery.
