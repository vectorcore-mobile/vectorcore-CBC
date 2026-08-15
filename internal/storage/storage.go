package storage

import (
	"context"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
)

type Record struct {
	Alert      cap.Alert `json:"alert"`
	ReceivedAt time.Time `json:"received_at"`
	State      string    `json:"state"`
}

type AuditEvent struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	AlertID string    `json:"alert_id,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

type Store interface {
	Migrate(context.Context) error
	LoadAlerts(context.Context) ([]Record, error)
	Upsert(context.Context, Record, []string) error
	Expire(context.Context, time.Time) ([]string, error)
	Audit(context.Context, int) ([]AuditEvent, error)
	SaveCBSPlan(context.Context, string, []byte) error
	CBSPlan(context.Context, string) ([]byte, error)
	AllocateCBSSerial(context.Context, string, uint16, uint8, bool) (uint16, error)
	Close() error
}
