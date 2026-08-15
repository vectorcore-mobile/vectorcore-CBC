package inventory

import "testing"

func TestGoSpatialMatcherCoverageIntersection(t *testing.T) {
	area, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	cell := LTECell{CoverageGeoJSON: `{"type":"Polygon","coordinates":[[[5,5],[5,15],[15,15],[15,5],[5,5]]]}`}
	m := NewGoSpatialMatcher()
	ok, reason := m.Evaluate(area, cell)
	if !ok || reason != ReasonCoverageIntersection {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}

func TestGoSpatialMatcherNonIntersectingCellExcluded(t *testing.T) {
	area, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	cell := LTECell{CoverageGeoJSON: `{"type":"Polygon","coordinates":[[[50,50],[50,60],[60,60],[60,50],[50,50]]]}`}
	m := NewGoSpatialMatcher()
	if ok, _ := m.Evaluate(area, cell); ok {
		t.Fatal("expected non-intersecting cell to be excluded")
	}
}

func TestGoSpatialMatcherCenterInsideFallback(t *testing.T) {
	area, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	lat, lon := 5.0, 5.0
	cell := LTECell{Latitude: &lat, Longitude: &lon}
	m := NewGoSpatialMatcher()
	ok, reason := m.Evaluate(area, cell)
	if !ok || reason != ReasonCenterInside {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}

func TestGoSpatialMatcherCenterOutsideExcluded(t *testing.T) {
	area, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	lat, lon := 50.0, 50.0
	cell := LTECell{Latitude: &lat, Longitude: &lon}
	m := NewGoSpatialMatcher()
	if ok, _ := m.Evaluate(area, cell); ok {
		t.Fatal("expected out-of-area center point to be excluded")
	}
}

func TestGoSpatialMatcherIgnoresCenterWhenCoverageGeometryPresent(t *testing.T) {
	area, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	// Center point would be inside, but non-empty coverage geometry that does
	// not intersect must take precedence per the conservative-intersection
	// policy: center-inside only applies when a cell has no coverage geometry.
	lat, lon := 5.0, 5.0
	cell := LTECell{
		Latitude: &lat, Longitude: &lon,
		CoverageGeoJSON: `{"type":"Polygon","coordinates":[[[50,50],[50,60],[60,60],[60,50],[50,50]]]}`,
	}
	m := NewGoSpatialMatcher()
	if ok, _ := m.Evaluate(area, cell); ok {
		t.Fatal("expected coverage geometry to take precedence over center point")
	}
}

func TestBuildMMEPlansGroupsAndDeduplicates(t *testing.T) {
	plmn := PLMN{MCC: "311", MNC: "435", MNCLength: 3}
	cells := []SelectedCell{
		{ID: 1, ECI: 100, TAC: 1, MMEName: "MME1"},
		{ID: 2, ECI: 101, TAC: 1, MMEName: "MME1"},
		{ID: 3, ECI: 102, TAC: 2, MMEName: "MME1"},
		{ID: 4, ECI: 200, TAC: 5, MMEName: "MME2"},
		{ID: 5, ECI: 200, TAC: 5, MMEName: "MME2"}, // duplicate ECI/TAC
	}
	plans := BuildMMEPlans(plmn, cells)
	if len(plans) != 2 {
		t.Fatalf("expected 2 MME plans, got %d: %+v", len(plans), plans)
	}
	byName := map[string]MMEPlan{}
	for _, p := range plans {
		byName[p.MMEName] = p
	}
	mme1 := byName["MME1"]
	if len(mme1.TAIs) != 2 || len(mme1.ECIs) != 3 {
		t.Fatalf("MME1 plan=%+v", mme1)
	}
	mme2 := byName["MME2"]
	if len(mme2.TAIs) != 1 || len(mme2.ECIs) != 1 {
		t.Fatalf("MME2 plan (dedup expected)=%+v", mme2)
	}
	if mme2.TAIs[0].MCC != "311" || mme2.TAIs[0].MNC != "435" {
		t.Fatalf("TAI PLMN not populated: %+v", mme2.TAIs[0])
	}
}
