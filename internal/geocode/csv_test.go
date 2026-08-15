package geocode

import (
	"strings"
	"testing"
)

const header = "mcc,mnc,mnc_length,eci,code_type,code\n"

func TestParseCSVValidRows(t *testing.T) {
	csv := header +
		"311,435,3,1048577,SAME,001101\n" +
		"311,435,3,1048577,ugc,ALZ057\n"
	result := ParseCSV(strings.NewReader(csv))
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", result.Errors)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(result.Rows))
	}
	if result.Rows[0].CodeType != SAME || result.Rows[0].Code != "001101" {
		t.Fatalf("row 0: %+v", result.Rows[0])
	}
	if result.Rows[1].CodeType != UGC || result.Rows[1].Code != "ALZ057" {
		t.Fatalf("row 1 (lowercase code_type should normalize to UGC): %+v", result.Rows[1])
	}
}

func TestParseCSVAcceptsAnyNonEmptyCodeType(t *testing.T) {
	csv := header + "311,435,3,1048577,COUNTY,001101\n"
	result := ParseCSV(strings.NewReader(csv))
	if len(result.Rows) != 1 {
		t.Fatalf("expected the row to be accepted (any type is valid now), got errors %+v", result.Errors)
	}
	if result.Rows[0].CodeType != "COUNTY" {
		t.Fatalf("expected CodeType %q, got %q", "COUNTY", result.Rows[0].CodeType)
	}
}

func TestParseCSVRejectsEmptyCodeType(t *testing.T) {
	csv := header + "311,435,3,1048577,,001101\n"
	result := ParseCSV(strings.NewReader(csv))
	if len(result.Rows) != 0 {
		t.Fatalf("expected the row to be rejected, got %+v", result.Rows)
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "invalid_code_type" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected invalid_code_type error, got %+v", result.Errors)
	}
}

func TestParseCSVRejectsMissingCode(t *testing.T) {
	csv := header + "311,435,3,1048577,SAME,\n"
	result := ParseCSV(strings.NewReader(csv))
	if len(result.Rows) != 0 {
		t.Fatalf("expected the row to be rejected, got %+v", result.Rows)
	}
}

func TestParseCSVRejectsDuplicateRow(t *testing.T) {
	csv := header +
		"311,435,3,1048577,SAME,001101\n" +
		"311,435,3,1048577,SAME,001101\n"
	result := ParseCSV(strings.NewReader(csv))
	if len(result.Rows) != 1 {
		t.Fatalf("got %d valid rows, want 1 (second is a duplicate)", len(result.Rows))
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "duplicate_row" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate_row error, got %+v", result.Errors)
	}
}

func TestParseCSVMissingHeaderIsFatal(t *testing.T) {
	result := ParseCSV(strings.NewReader("mcc,mnc,eci,code_type,code\n311,435,1048577,SAME,001101\n"))
	if len(result.Rows) != 0 {
		t.Fatalf("expected no rows parsed on missing header, got %+v", result.Rows)
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "missing_header" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_header error, got %+v", result.Errors)
	}
}

func TestParseCSVMalformedRowFieldCount(t *testing.T) {
	result := ParseCSV(strings.NewReader(header + "311,435,3,1048577,SAME\n"))
	if len(result.Rows) != 0 {
		t.Fatalf("expected no rows, got %+v", result.Rows)
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "field_count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected field_count error, got %+v", result.Errors)
	}
}
