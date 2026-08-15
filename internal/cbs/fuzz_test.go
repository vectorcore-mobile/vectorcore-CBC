package cbs

import "testing"

func FuzzEncode(f *testing.F) {
	f.Add("Flood warning", "en")
	f.Add("警報", "ja")
	f.Add("", "en")
	f.Fuzz(func(t *testing.T, text, language string) {
		pages, _, _, err := Encode(text, language)
		if err == nil {
			if len(pages) == 0 || len(pages) > MaxPages {
				t.Fatalf("invalid page count %d", len(pages))
			}
			for _, p := range pages {
				if len(p.Data) != PageOctets {
					t.Fatalf("invalid page size %d", len(p.Data))
				}
			}
		}
	})
}
