// Package geocode maps operator-curated geocodes (SAME, UGC, or any other
// scheme a CAP <geocode valueName="..."> may carry) to the specific LTE
// cells they cover, so a CAP alert carrying only a <geocode> (no polygon) -
// the common case for real NWS/WEA alerts - can still be targeted to real
// cells. Unlike internal/inventory, this deliberately does not model or
// store boundary polygons themselves: an operator hand-tags each of their
// own cells with the code(s) it falls under, and resolution at alert time
// is a flat lookup, not a geometry computation.
package geocode

import "time"

// CodeType is a CAP geocode <geocode valueName="..."> taxonomy - any
// scheme an operator registers in the Geo Codes registry (see Code), not a
// closed set. SAME and UGC below are common examples, not an exhaustive
// list. Literal cell/tracking-area identifiers (cell/tac/cgi/etc.) are
// handled elsewhere (see internal/cbs.uniqueGeocodes) and never reach this
// package.
type CodeType string

const (
	SAME CodeType = "SAME"
	UGC  CodeType = "UGC"
)

// Code is one Geo Codes registry entry: an operator-curated (type, code)
// definition with a human-readable description, independent of any cell.
// Cell association happens separately via Entry.
type Code struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	Code        string    `json:"code"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Entry is one (cell, code) mapping row.
type Entry struct {
	ID        int64     `json:"id"`
	CellID    int64     `json:"cellId"`
	ECI       uint32    `json:"eci"`                // denormalized for display/export
	CellName  string    `json:"cellName,omitempty"` // denormalized
	CodeType  CodeType  `json:"codeType"`
	Code      string    `json:"code"`
	CreatedAt time.Time `json:"createdAt"`
}

// Filter narrows ListEntries; zero values mean "no restriction".
type Filter struct {
	CodeType      string
	Code          string
	CellID        *int64
	Limit, Offset int
}

type ImportMode string

const (
	ValidateOnly ImportMode = "validate-only"
	Merge        ImportMode = "merge"   // upsert; rows not present in the file are left untouched
	Replace      ImportMode = "replace" // wipe the table, then reinsert the file's rows
)

// ImportResult reports what a CSV import parsed and (for merge/replace)
// applied. Unlike internal/inventory's Import, this is synchronous and
// unpersisted - there is no audit-log table for geocode imports, since this
// table is small and hand-curated rather than bulk-replaced from an
// external system of record.
type ImportResult struct {
	RowsReceived, RowsValid, RowsRejected, Inserted, Deleted int
	Errors                                                   []RowError
}

type RowError struct {
	Row     int    `json:"row,omitempty"`
	Column  string `json:"column,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
