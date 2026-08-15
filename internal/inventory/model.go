// Package inventory contains operator-owned LTE cell inventory contracts.
package inventory

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID returns an opaque, sufficiently unique identifier for import and
// version audit rows. It is not a ULID; ordering relies on CreatedAt.
func NewID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("inventory: crypto/rand unavailable: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(b)
}

type PLMN struct {
	MCC       string `json:"mcc"`
	MNC       string `json:"mnc"`
	MNCLength int    `json:"mncLength"`
}
type Bounds struct {
	MinLatitude  float64 `json:"minLatitude"`
	MinLongitude float64 `json:"minLongitude"`
	MaxLatitude  float64 `json:"maxLatitude"`
	MaxLongitude float64 `json:"maxLongitude"`
}
type LTECell struct {
	ID              int64     `json:"id"`
	PLMN            PLMN      `json:"plmn"`
	ECI             uint32    `json:"eci"`
	ENBID           uint32    `json:"enbId"`
	LocalCellID     uint8     `json:"localCellId"`
	TAC             uint16    `json:"tac"`
	CellName        string    `json:"cellName,omitempty"`
	MMEName         string    `json:"mmeName,omitempty"`
	Latitude        *float64  `json:"latitude,omitempty"`
	Longitude       *float64  `json:"longitude,omitempty"`
	NominalRadiusM  *float64  `json:"nominalRadiusM,omitempty"`
	AzimuthDeg      *float64  `json:"azimuthDeg,omitempty"`
	BeamwidthDeg    *float64  `json:"beamwidthDeg,omitempty"`
	CoverageGeoJSON string    `json:"coverageGeoJSON,omitempty"`
	Bounds          *Bounds   `json:"bounds,omitempty"`
	GeometryQuality string    `json:"geometryQuality"`
	Source          string    `json:"source,omitempty"`
	SourceRecordID  string    `json:"sourceRecordId,omitempty"`
	SourceVersion   string    `json:"sourceVersion,omitempty"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
type ImportMode string

const (
	ValidateOnly ImportMode = "validate-only"
	Merge        ImportMode = "merge"
	Replace      ImportMode = "replace"
)

type ApplyResult struct {
	Inserted    int `json:"inserted"`
	Updated     int `json:"updated"`
	Deactivated int `json:"deactivated"`
}

// ImportStatus reflects the lifecycle of an inventory_imports audit row.
type ImportStatus string

const (
	ImportStatusPending   ImportStatus = "pending"
	ImportStatusValidated ImportStatus = "validated"
	ImportStatusCompleted ImportStatus = "completed"
	ImportStatusFailed    ImportStatus = "failed"
)

// InventoryImport is the audit record for one CSV import request, regardless
// of mode or outcome.
type InventoryImport struct {
	ID                 string       `json:"id"`
	InventoryVersionID string       `json:"inventoryVersionId,omitempty"`
	SourceFilename     string       `json:"sourceFilename"`
	SourceSHA256       string       `json:"sourceSha256"`
	Mode               ImportMode   `json:"mode"`
	Status             ImportStatus `json:"status"`
	RowsReceived       int          `json:"rowsReceived"`
	RowsValid          int          `json:"rowsValid"`
	RowsRejected       int          `json:"rowsRejected"`
	InsertedCount      int          `json:"inserted"`
	UpdatedCount       int          `json:"updated"`
	DeactivatedCount   int          `json:"deactivated"`
	WarningCount       int          `json:"warnings"`
	CreatedAt          time.Time    `json:"createdAt"`
	CompletedAt        *time.Time   `json:"completedAt,omitempty"`
}

// InventoryVersion records one applied merge/replace import as the current
// (or a historical) active inventory snapshot.
type InventoryVersion struct {
	ID             string     `json:"id"`
	VersionName    string     `json:"versionName"`
	SourceFilename string     `json:"sourceFilename,omitempty"`
	SourceSHA256   string     `json:"sourceSha256"`
	ImportMode     ImportMode `json:"importMode"`
	RecordCount    int        `json:"recordCount"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// CellFilter bounds ListCells/ExportCells/candidate queries. Zero values mean
// "no filter" for that field; Limit<=0 means the repository default applies.
type CellFilter struct {
	Active  *bool
	MCC     string
	MNC     string
	TAC     *uint16
	MMEName string
	ENBID   *uint32
	ECI     *uint32
	Limit   int
	Offset  int
}

// ApplyImportInput carries a fully validated, normalized set of cells into
// the repository's transactional merge/replace application.
type ApplyImportInput struct {
	ImportID       string
	Mode           ImportMode
	Cells          []LTECell
	VersionName    string
	SourceFilename string
	SourceSHA256   string
}

// ExportMeta describes the inventory snapshot an export represents, used to
// populate response headers.
type ExportMeta struct {
	VersionName string
	ExportedAt  time.Time
	RecordCount int
}
