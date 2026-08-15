// Package cbs prepares TS 23.041 CBS payloads. It does not send them to RAN
// peers; the SBcAP transport consumes these plans in the delivery phase.
package cbs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/inventory"
)

const (
	PageOctets              = 82
	MaxPages                = 15
	ImmediateCellWide uint8 = 0
	PLMNWide          uint8 = 1
	TrackingAreaWide  uint8 = 2
	CellWide          uint8 = 3
)

type Repository interface {
	AllocateCBSSerial(context.Context, string, uint16, uint8, bool) (uint16, error)
	SaveCBSPlan(context.Context, string, []byte) error
}

// CellSelector resolves a geographic area to LTE cells. *inventory.Service
// satisfies this directly. It is optional (see SetCellSelector) - without
// one, target() falls back to its original geocode-only behavior.
type CellSelector interface {
	SelectionPreview(ctx context.Context, req inventory.SelectionRequest) (*inventory.SelectionResult, error)
}

// GeocodeResolver resolves a CAP <geocode> (any valueName/type an operator
// has registered) to the operator's own cells that fall under it.
// *geocode.Service satisfies this directly. It is optional (see
// SetGeocodeResolver) - without one, target() ignores all geocodes exactly
// as it did before this existed.
type GeocodeResolver interface {
	ResolveCells(ctx context.Context, codeType, code string) ([]uint32, error)
}
type Target struct {
	Scope         uint8    `json:"scope"`
	Cells         []string `json:"cells,omitempty"`
	TrackingAreas []string `json:"tracking_areas,omitempty"`
}
type Page struct {
	Number        uint8  `json:"number"`
	Total         uint8  `json:"total"`
	PageParameter uint8  `json:"page_parameter"`
	Data          []byte `json:"data"`
}
type Message struct {
	InfoIndex         int    `json:"info_index"`
	MessageIdentifier uint16 `json:"message_identifier"`
	SerialNumber      uint16 `json:"serial_number"`
	DCS               byte   `json:"dcs"`
	Encoding          string `json:"encoding"`
	Target            Target `json:"target"`
	Pages             []Page `json:"pages"`
}
type Plan struct {
	AlertIdentifier string    `json:"alert_identifier"`
	Messages        []Message `json:"messages"`
}
type Preparer struct {
	cfg      config.CBS
	repo     Repository
	plmn     string
	selector CellSelector
	geocodes GeocodeResolver
}

func New(cfg config.CBS, repo Repository) *Preparer { return &Preparer{cfg: cfg, repo: repo} }

// SetCellSelector wires polygon-based area targeting: any CAP Area with a
// <polygon> is resolved to real LTE cells via sel (typically
// *inventory.Service), scoped to plmn (config.SBcAP.PLMN's "MCC-MNC"
// string). Optional - a Preparer with no selector set keeps its original
// geocode-only behavior (see target()), which is what every existing
// Preparer built via New alone still gets.
func (p *Preparer) SetCellSelector(plmn string, sel CellSelector) {
	p.plmn, p.selector = plmn, sel
}

// SetGeocodeResolver wires geocode-based area targeting: any CAP <geocode>
// is resolved to real LTE cells via r (typically *geocode.Service), which
// looks up the geocode's (valueName, value) pair against the operator's Geo
// Codes registry and cell mappings - any type, not just SAME/UGC. An
// unrecognized type/code pair simply resolves to zero cells, not an error.
// Optional - a Preparer with no resolver set keeps ignoring geocodes
// entirely, which is what every existing Preparer built via New alone
// still gets.
func (p *Preparer) SetGeocodeResolver(r GeocodeResolver) {
	p.geocodes = r
}

// Publish satisfies service.Publisher and persists a prepared delivery plan.
func (p *Preparer) Publish(a cap.Alert) error {
	_, err := p.Prepare(context.Background(), a)
	return err
}
func (p *Preparer) Prepare(ctx context.Context, a cap.Alert) (Plan, error) {
	if a.MsgType == "Cancel" {
		return Plan{AlertIdentifier: a.Identifier}, nil
	}
	plan := Plan{AlertIdentifier: a.Identifier}
	serials := map[string]uint16{}
	for i, info := range a.Info {
		target, err := p.target(ctx, info.Areas)
		if err != nil {
			return Plan{}, fmt.Errorf("CAP info %d target: %w", i, err)
		}
		messageID, err := p.messageIdentifier(a.Status, info)
		if err != nil {
			return Plan{}, fmt.Errorf("CAP info %d: %w", i, err)
		}
		key := a.Identifier
		isUpdate := a.MsgType == "Update" && len(a.ReferenceIDs()) > 0
		if isUpdate {
			key = a.ReferenceIDs()[0]
		}
		allocationKey := fmt.Sprintf("%s/%04x/%d", key, messageID, target.Scope)
		serial, ok := serials[allocationKey]
		if !ok {
			serial, err = p.repo.AllocateCBSSerial(ctx, allocationKey, messageID, target.Scope, isUpdate)
			if err != nil {
				return Plan{}, err
			}
			serials[allocationKey] = serial
		}
		pages, dcs, encoding, err := Encode(text(info), info.Language)
		if err != nil {
			return Plan{}, fmt.Errorf("CAP info %d: %w", i, err)
		}
		plan.Messages = append(plan.Messages, Message{InfoIndex: i, MessageIdentifier: messageID, SerialNumber: serial, DCS: dcs, Encoding: encoding, Target: target, Pages: pages})
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, err
	}
	if err = p.repo.SaveCBSPlan(ctx, a.Identifier, raw); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// messageIdentifier picks the CMAS/WEA Message Identifier for one CAP info
// block. classify() determines the standard category (if any); the
// hardcoded TS 23.041 table (atisMessageIdentifiers) is authoritative for
// the categories it covers - including the WEA 3.0 additions public-safety
// (0x112C) and state-local-test (0x112E) - with cfg.MessageIdentifiers
// available as a rare manual override keyed by classification label. Any
// classification label somehow absent from both falls back to erroring
// rather than guessing, consistent with this package's other "can't
// confidently resolve this" rejections (e.g. target()'s unrecognised-geocode
// error).
func (p *Preparer) messageIdentifier(status string, info cap.Info) (uint16, error) {
	label := classify(status, info)
	if label == "" {
		return p.cfg.DefaultMessageIdentifier, nil
	}
	if id, ok := p.cfg.MessageIdentifiers[label]; ok {
		return id, nil
	}
	if id, ok := atisMessageIdentifiers[label]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("CAP alert classified as %q, which has no standardised CMAS Message Identifier yet - set cbs.message_identifiers.%s explicitly", label, label)
}
func (p *Preparer) target(ctx context.Context, areas []cap.Area) (Target, error) {
	cells, tais := uniqueGeocodes(areas)
	polyCells, err := p.polygonCells(ctx, areas)
	if err != nil {
		return Target{}, err
	}
	circleCells, err := p.circleCells(ctx, areas)
	if err != nil {
		return Target{}, err
	}
	namedCells, err := p.namedGeocodeCells(ctx, areas)
	if err != nil {
		return Target{}, err
	}
	cells = mergeUnique(mergeUnique(mergeUnique(cells, polyCells), circleCells), namedCells)
	if len(cells) > 0 && len(tais) > 0 {
		return Target{}, fmt.Errorf("mixed cell and tracking-area geocodes are not supported in one CBS message")
	}
	if len(cells) > 0 {
		return Target{Scope: CellWide, Cells: cells}, nil
	}
	if len(tais) > 0 {
		return Target{Scope: TrackingAreaWide, TrackingAreas: tais}, nil
	}
	if p.cfg.AllowPLMNWide {
		return Target{Scope: PLMNWide}, nil
	}
	return Target{}, fmt.Errorf("no recognised cell or tracking-area geocode (PLMN-wide delivery is disabled)")
}

// polygonCells resolves each area's CAP <polygon> entries (any number of
// them, per area) to LTE cell IDs via the configured CellSelector,
// returning deduplicated decimal ECI strings matching Target.Cells'
// existing convention. Returns nil, nil unchanged when no selector is
// wired (SetCellSelector never called) or no area has a polygon,
// preserving today's geocode-only behavior exactly.
func (p *Preparer) polygonCells(ctx context.Context, areas []cap.Area) ([]string, error) {
	return p.shapeCells(ctx, areas, "polygon",
		func(a cap.Area) []string { return a.Polygons }, cap.PolygonToGeoJSON)
}

// circleCells is polygonCells' counterpart for CAP <circle> entries -
// same aggregation, but each circle is first approximated to a polygon via
// cap.CircleToGeoJSON (see its doc comment for why) before being run
// through the identical CellSelector path.
func (p *Preparer) circleCells(ctx context.Context, areas []cap.Area) ([]string, error) {
	return p.shapeCells(ctx, areas, "circle",
		func(a cap.Area) []string { return a.Circles }, cap.CircleToGeoJSON)
}

// shapeCells is the shared aggregation loop behind polygonCells/
// circleCells: for every area, convert each of its shapes (as selected by
// shapesOf) to GeoJSON (via toGeoJSON) and resolve it through the
// configured CellSelector, deduplicating into one decimal-ECI-string
// slice. label only affects error messages.
func (p *Preparer) shapeCells(ctx context.Context, areas []cap.Area, label string, shapesOf func(cap.Area) []string, toGeoJSON func(string) ([]byte, error)) ([]string, error) {
	if p.selector == nil {
		return nil, nil
	}
	var plmn inventory.PLMN
	var plmnParsed bool
	seen := map[string]bool{}
	var cells []string
	for _, area := range areas {
		for _, shape := range shapesOf(area) {
			if !plmnParsed {
				var err error
				plmn, err = parsePLMN(p.plmn)
				if err != nil {
					return nil, fmt.Errorf("cell selection PLMN: %w", err)
				}
				plmnParsed = true
			}
			geoJSON, err := toGeoJSON(shape)
			if err != nil {
				return nil, fmt.Errorf("CAP %s: %w", label, err)
			}
			result, err := p.selector.SelectionPreview(ctx, inventory.SelectionRequest{
				PLMN: plmn, Policy: inventory.PolicyConservativeIntersection, Area: geoJSON,
			})
			if err != nil {
				return nil, fmt.Errorf("%s cell selection: %w", label, err)
			}
			for _, c := range result.Cells {
				v := strconv.FormatUint(uint64(c.ECI), 10)
				if !seen[v] {
					cells, seen[v] = append(cells, v), true
				}
			}
		}
	}
	return cells, nil
}

// namedGeocodeCells resolves each area's <geocode> entries (if any), of any
// type, via the configured GeocodeResolver, returning deduplicated decimal
// ECI strings matching Target.Cells' existing convention. Returns nil, nil
// unchanged when no resolver is wired (SetGeocodeResolver never called) or
// no area has a geocode, preserving today's behavior exactly. A type/code
// pair the resolver doesn't recognize simply contributes no cells - it's
// not an error, matching ResolveCells' existing "zero matches isn't an
// error" contract.
func (p *Preparer) namedGeocodeCells(ctx context.Context, areas []cap.Area) ([]string, error) {
	if p.geocodes == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var cells []string
	for _, area := range areas {
		for _, g := range area.Geocodes {
			n := strings.ToUpper(strings.TrimSpace(g.ValueName))
			if n == "" {
				continue
			}
			value := strings.TrimSpace(g.Value)
			if value == "" {
				continue
			}
			ecis, err := p.geocodes.ResolveCells(ctx, n, value)
			if err != nil {
				return nil, fmt.Errorf("geocode %s %q: %w", n, value, err)
			}
			for _, eci := range ecis {
				v := strconv.FormatUint(uint64(eci), 10)
				if !seen[v] {
					cells, seen[v] = append(cells, v), true
				}
			}
		}
	}
	return cells, nil
}

func mergeUnique(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			a, seen[v] = append(a, v), true
		}
	}
	return a
}

// parsePLMN splits config.SBcAP.PLMN's "MCC-MNC" string (e.g. "311-435",
// already validated by internal/config) into an inventory.PLMN, inferring
// MNCLength from the MNC's digit count.
func parsePLMN(s string) (inventory.PLMN, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 || len(parts[0]) != 3 || (len(parts[1]) != 2 && len(parts[1]) != 3) {
		return inventory.PLMN{}, fmt.Errorf("invalid PLMN %q, expected MCC-MNC (e.g. 311-435)", s)
	}
	return inventory.PLMN{MCC: parts[0], MNC: parts[1], MNCLength: len(parts[1])}, nil
}
func uniqueGeocodes(areas []cap.Area) (cells, tais []string) {
	seenC, seenT := map[string]bool{}, map[string]bool{}
	for _, area := range areas {
		for _, g := range area.Geocodes {
			n, v := strings.ToLower(strings.TrimSpace(g.ValueName)), strings.TrimSpace(g.Value)
			if v == "" {
				continue
			}
			switch n {
			case "cell", "cell_id", "cgi", "ecgi", "nci", "nr-cgi":
				if !seenC[v] {
					cells, seenC[v] = append(cells, v), true
				}
			case "tac", "tai", "tracking_area", "tracking-area":
				if !seenT[v] {
					tais, seenT[v] = append(tais, v), true
				}
			}
		}
	}
	return
}
func text(i cap.Info) string {
	parts := []string{}
	for _, v := range []string{i.Headline, i.Description, i.Instruction} {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(i.Event)
	}
	return strings.Join(parts, "\n")
}
