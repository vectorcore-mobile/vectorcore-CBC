package cap

import "testing"

func FuzzParse(f *testing.F) {
	f.Add([]byte(`<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><identifier>a</identifier><sender>cbe</sender><sent>2026-01-01T00:00:00Z</sent><status>Actual</status><msgType>Alert</msgType><scope>Public</scope><info><event>test</event></info></alert>`))
	f.Add([]byte("not XML"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = Parse(raw)
	})
}
