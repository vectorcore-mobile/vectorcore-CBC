package inventory

import (
	"encoding/json"
	"sort"
)

// PolicyConservativeIntersection is the only selection policy this initial
// version supports: select a cell when its stored coverage geometry
// intersects the requested area, or - only when a cell has no coverage
// geometry at all - when its center point lies inside the requested area.
const PolicyConservativeIntersection = "conservative-intersection"

const (
	ReasonCoverageIntersection = "coverage_intersection"
	ReasonCenterInside         = "center_inside"
)

// SelectionRequest is the selection-preview HTTP request body.
type SelectionRequest struct {
	PLMN   PLMN            `json:"plmn"`
	Policy string          `json:"policy"`
	Area   json.RawMessage `json:"area"`
}

type SelectedCell struct {
	ID              int64  `json:"id"`
	ECI             uint32 `json:"eci"`
	TAC             uint16 `json:"tac"`
	ENBID           uint32 `json:"enbId"`
	MMEName         string `json:"mmeName"`
	GeometryQuality string `json:"geometryQuality"`
	SelectionReason string `json:"selectionReason"`
}

type TAI struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
	TAC uint16 `json:"tac"`
}

type MMEPlan struct {
	MMEName string   `json:"mmeName"`
	TAIs    []TAI    `json:"tais"`
	ECIs    []uint32 `json:"ecis"`
}

// SelectionResult is the selection-preview HTTP response body. This is a
// preview only: nothing here ever transmits SBcAP.
type SelectionResult struct {
	InventoryVersion string         `json:"inventoryVersion"`
	Policy           string         `json:"policy"`
	CandidateCount   int            `json:"candidateCount"`
	SelectedCount    int            `json:"selectedCount"`
	Cells            []SelectedCell `json:"cells"`
	MMEPlans         []MMEPlan      `json:"mmePlans"`
	Warnings         []string       `json:"warnings"`
}

// SpatialMatcher decides whether a candidate cell is selected for a
// validated request area. It is the seam a future PostGIS-backed matcher
// implements instead of this initial pure-Go one.
type SpatialMatcher interface {
	Evaluate(requestArea Geometry, cell LTECell) (selected bool, reason string)
}

type goSpatialMatcher struct{}

// NewGoSpatialMatcher returns the initial pure-Go conservative-intersection
// matcher.
func NewGoSpatialMatcher() SpatialMatcher { return goSpatialMatcher{} }

func (goSpatialMatcher) Evaluate(requestArea Geometry, cell LTECell) (bool, string) {
	if cell.CoverageGeoJSON != "" {
		cellGeom, err := ParseGeometry([]byte(cell.CoverageGeoJSON))
		if err != nil {
			return false, ""
		}
		if Intersects(requestArea, cellGeom) {
			return true, ReasonCoverageIntersection
		}
		return false, ""
	}
	if cell.Latitude != nil && cell.Longitude != nil && requestArea.ContainsPoint(*cell.Longitude, *cell.Latitude) {
		return true, ReasonCenterInside
	}
	return false, ""
}

// BuildMMEPlans groups selected cells by MME, deduplicating TACs (as TAIs
// for the request PLMN) and ECIs, in a deterministic sorted order.
func BuildMMEPlans(plmn PLMN, cells []SelectedCell) []MMEPlan {
	type accum struct {
		tacs map[uint16]bool
		ecis map[uint32]bool
	}
	plans := map[string]*accum{}
	var order []string
	for _, c := range cells {
		name := c.MMEName
		a, ok := plans[name]
		if !ok {
			a = &accum{tacs: map[uint16]bool{}, ecis: map[uint32]bool{}}
			plans[name] = a
			order = append(order, name)
		}
		a.tacs[c.TAC] = true
		a.ecis[c.ECI] = true
	}
	sort.Strings(order)
	out := make([]MMEPlan, 0, len(order))
	for _, name := range order {
		a := plans[name]
		tacs := make([]uint16, 0, len(a.tacs))
		for t := range a.tacs {
			tacs = append(tacs, t)
		}
		sort.Slice(tacs, func(i, j int) bool { return tacs[i] < tacs[j] })
		tais := make([]TAI, 0, len(tacs))
		for _, t := range tacs {
			tais = append(tais, TAI{MCC: plmn.MCC, MNC: plmn.MNC, TAC: t})
		}
		ecis := make([]uint32, 0, len(a.ecis))
		for e := range a.ecis {
			ecis = append(ecis, e)
		}
		sort.Slice(ecis, func(i, j int) bool { return ecis[i] < ecis[j] })
		out = append(out, MMEPlan{MMEName: name, TAIs: tais, ECIs: ecis})
	}
	return out
}
