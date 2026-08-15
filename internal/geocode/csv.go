package geocode

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVColumns is the canonical, stable interchange column order, matching
// internal/inventory/csv.go's convention: headers accepted case-insensitively
// with surrounding whitespace trimmed; exports always emit exactly these
// names in this order. The cell is identified the same way lte_cells' unique
// index is (mcc/mnc/mnc_length/eci) rather than by internal row ID, so
// operators reuse what they already know from the cell-inventory CSV.
var CSVColumns = []string{"mcc", "mnc", "mnc_length", "eci", "code_type", "code"}

const maxCSVErrors = 1000

// PendingRow is one validated CSV row, not yet resolved to a cell_id (that
// lookup happens against lte_cells inside ApplyImport).
type PendingRow struct {
	MCC       string
	MNC       string
	MNCLength int
	ECI       uint32
	CodeType  CodeType
	Code      string
}

type ParseResult struct {
	Rows     []PendingRow // valid rows only
	RowCount int          // total data rows read, including rejected ones
	Errors   []RowError
}

// ParseCSV validates the complete file before any repository mutation.
// Unknown, missing, or duplicate headers are fatal for the whole file;
// row-level problems are collected per row so operators see every issue in
// one pass, up to maxCSVErrors.
func ParseCSV(r io.Reader) ParseResult {
	rr := csv.NewReader(r)
	rr.FieldsPerRecord = -1
	rows, err := rr.ReadAll()
	var out ParseResult
	if err != nil {
		out.Errors = []RowError{{Row: 1, Code: "malformed_csv", Message: err.Error()}}
		return out
	}
	if len(rows) == 0 {
		out.Errors = []RowError{{Row: 1, Code: "empty_file", Message: "CSV is empty"}}
		return out
	}
	idx := map[string]int{}
	for i, v := range rows[0] {
		k := strings.ToLower(strings.TrimSpace(v))
		if _, ok := idx[k]; ok {
			out.Errors = append(out.Errors, RowError{Row: 1, Column: k, Code: "duplicate_header", Message: "duplicate header"})
		}
		idx[k] = i
	}
	for _, k := range CSVColumns {
		if _, ok := idx[k]; !ok {
			out.Errors = append(out.Errors, RowError{Row: 1, Column: k, Code: "missing_header", Message: "required canonical header missing"})
		}
	}
	for k := range idx {
		found := false
		for _, want := range CSVColumns {
			if k == want {
				found = true
			}
		}
		if !found {
			out.Errors = append(out.Errors, RowError{Row: 1, Column: k, Code: "unknown_header", Message: "unknown header"})
		}
	}
	if len(out.Errors) > 0 {
		return out
	}
	seen := map[string]int{}
	for n, row := range rows[1:] {
		if len(out.Errors) >= maxCSVErrors {
			out.Errors = append(out.Errors, RowError{Code: "too_many_errors", Message: "validation stopped after 1000 errors; fix the reported issues and re-submit"})
			break
		}
		out.RowCount++
		if len(row) != len(rows[0]) {
			out.Errors = append(out.Errors, RowError{Row: n + 2, Code: "field_count", Message: "unexpected field count"})
			continue
		}
		get := func(k string) string { return strings.TrimSpace(row[idx[k]]) }
		pr, fieldErrs := rowFrom(get)
		if len(fieldErrs) > 0 {
			for _, fe := range fieldErrs {
				out.Errors = append(out.Errors, RowError{Row: n + 2, Column: fe.Column, Code: fe.Code, Message: fe.Message})
			}
			continue
		}
		key := fmt.Sprintf("%s-%s-%d-%d-%s-%s", pr.MCC, pr.MNC, pr.MNCLength, pr.ECI, pr.CodeType, pr.Code)
		if prev, ok := seen[key]; ok {
			out.Errors = append(out.Errors, RowError{Row: n + 2, Code: "duplicate_row", Message: fmt.Sprintf("identical (cell, code_type, code) also appears on row %d", prev)})
			continue
		}
		seen[key] = n + 2
		out.Rows = append(out.Rows, pr)
	}
	return out
}

type fieldError struct{ Column, Code, Message string }

func rowFrom(get func(string) string) (PendingRow, []fieldError) {
	var errs []fieldError
	add := func(column, code, message string) { errs = append(errs, fieldError{column, code, message}) }
	var pr PendingRow

	mcc, mnc := get("mcc"), get("mnc")
	if len(mcc) != 3 || !digits(mcc) {
		add("mcc", "invalid_mcc", "MCC must be exactly three decimal digits")
	} else {
		pr.MCC = mcc
	}
	if (len(mnc) != 2 && len(mnc) != 3) || !digits(mnc) {
		add("mnc", "invalid_mnc", "MNC must be two or three decimal digits")
	} else {
		pr.MNC = mnc
	}
	if mncLen, err := atoi(get("mnc_length"), 2, 3); err != nil {
		add("mnc_length", "invalid_mnc_length", "mnc_length must be 2 or 3")
	} else {
		pr.MNCLength = mncLen
		if len(mnc) > 0 && mncLen != len(mnc) {
			add("mnc_length", "mnc_length_mismatch", "mnc_length must match the number of digits in mnc")
		}
	}
	if eciVal, err := atoi(get("eci"), 0, 268435455); err != nil {
		add("eci", "invalid_eci", "ECI must be in the 28-bit range 0-268435455")
	} else {
		pr.ECI = uint32(eciVal)
	}

	codeType := strings.ToUpper(strings.TrimSpace(get("code_type")))
	if codeType == "" {
		add("code_type", "invalid_code_type", "code_type is required")
	} else {
		pr.CodeType = CodeType(codeType)
	}

	if code := get("code"); code == "" {
		add("code", "missing_code", "code is required")
	} else {
		pr.Code = code
	}

	return pr, errs
}

func atoi(s string, min, max int) (int, error) {
	v, e := strconv.Atoi(s)
	if e != nil || v < min || v > max {
		return 0, fmt.Errorf("out of range")
	}
	return v, nil
}
func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
