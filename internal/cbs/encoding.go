package cbs

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var gsmAlphabet = func() map[rune]byte {
	const alphabet = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	m := make(map[rune]byte, len(alphabet))
	for i, r := range []rune(alphabet) {
		if r != 0x1b {
			m[r] = byte(i)
		}
	}
	return m
}()
var gsmExtension = map[rune]byte{'\f': 0x0a, '^': 0x14, '{': 0x28, '}': 0x29, '\\': 0x2f, '[': 0x3c, '~': 0x3d, ']': 0x3e, '|': 0x40, '€': 0x65}
var languageDCS = map[string]byte{"de": 0x00, "en": 0x01, "it": 0x02, "fr": 0x03, "es": 0x04, "nl": 0x05, "sv": 0x06, "da": 0x07, "pt": 0x08, "fi": 0x09, "no": 0x0a, "el": 0x0b, "tr": 0x0c, "hu": 0x0d, "pl": 0x0e, "cs": 0x20, "he": 0x21, "ar": 0x22, "ru": 0x23, "is": 0x24}

// Encode returns fixed 82-octet TS 23.041 content pages. GSM 7-bit is used
// whenever representable; otherwise the message uses UCS-2 (DCS 0x11).
func Encode(text, language string) ([]Page, byte, string, error) {
	// Bound work before GSM/UCS-2 conversion. A legal CBS message holds at most
	// 15 GSM pages of 93 septets; accepting arbitrarily large CAP text here
	// would permit an unauthenticated CBE payload to consume CPU and memory.
	if utf8.RuneCountInString(text) > MaxPages*93 {
		return nil, 0, "", fmt.Errorf("CBS message exceeds %d character input limit", MaxPages*93)
	}
	if septets, err := gsmSeptets(text); err == nil {
		pages, err := gsmPages(septets)
		return pages, dcs(language), "gsm7", err
	}
	pages, err := ucs2Pages(text)
	return pages, 0x11, "ucs2", err
}
func dcs(language string) byte {
	if d, ok := languageDCS[strings.ToLower(language)]; ok {
		return d
	}
	return 0x0f
}
func gsmSeptets(text string) ([]byte, error) {
	var out []byte
	for _, r := range text {
		if v, ok := gsmAlphabet[r]; ok {
			out = append(out, v)
			continue
		}
		if v, ok := gsmExtension[r]; ok {
			out = append(out, 0x1b, v)
			continue
		}
		return nil, fmt.Errorf("character %q is not in GSM 7-bit alphabet", r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("CBS message text is empty")
	}
	return out, nil
}
func gsmPages(septets []byte) ([]Page, error) {
	const perPage = 93
	pages := make([]Page, 0, (len(septets)+perPage-1)/perPage)
	for len(septets) > 0 {
		n := perPage
		if len(septets) < n {
			n = len(septets)
		}
		data := packSeptets(septets[:n])
		data = append(data, make([]byte, PageOctets-len(data))...)
		for i := len(packSeptets(septets[:n])); i < PageOctets; i++ {
			data[i] = 0x0d
		}
		pages = append(pages, Page{Data: data})
		septets = septets[n:]
	}
	return finishPages(pages)
}
func packSeptets(in []byte) []byte {
	out := make([]byte, (len(in)*7+7)/8)
	for i, v := range in {
		bit := uint((i * 7) % 8)
		j := i * 7 / 8
		out[j] |= v << bit
		if bit > 1 && j+1 < len(out) {
			out[j+1] |= v >> (8 - bit)
		}
	}
	return out
}
func ucs2Pages(text string) ([]Page, error) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, fmt.Errorf("CBS message text is empty")
	}
	pages := make([]Page, 0, (len(runes)+40)/41)
	for len(runes) > 0 {
		n := 41
		if len(runes) < n {
			n = len(runes)
		}
		data := make([]byte, 0, PageOctets)
		for _, r := range runes[:n] {
			if r > 0xffff || utf16.IsSurrogate(r) {
				return nil, fmt.Errorf("character %q cannot be represented in UCS-2", r)
			}
			data = append(data, byte(r>>8), byte(r))
		}
		for len(data) < PageOctets {
			data = append(data, 0x00, 0x0d)
		}
		pages = append(pages, Page{Data: data})
		runes = runes[n:]
	}
	return finishPages(pages)
}
func finishPages(pages []Page) ([]Page, error) {
	if len(pages) == 0 || len(pages) > MaxPages {
		return nil, fmt.Errorf("CBS message requires %d pages; maximum is %d", len(pages), MaxPages)
	}
	for i := range pages {
		pages[i].Number, pages[i].Total = uint8(i+1), uint8(len(pages))
		pages[i].PageParameter = pages[i].Number<<4 | pages[i].Total
	}
	return pages, nil
}
