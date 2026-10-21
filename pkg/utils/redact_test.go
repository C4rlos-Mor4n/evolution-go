package utils

import "testing"

func TestMaskID(t *testing.T) {
	cases := map[string]string{
		"":                               "",
		"593900001234":                   "********1234",
		"593900001234@s.whatsapp.net":    "********1234@s.whatsapp.net",
		"593900001234:12@s.whatsapp.net": "********1234:12@s.whatsapp.net",
		"123456789012345@lid":            "***********2345@lid",
		"120363012345678901@g.us":        "**************8901@g.us",
		"abc":                            "***",
	}
	for in, want := range cases {
		if got := MaskID(in); got != want {
			t.Errorf("MaskID(%q) = %q, want %q", in, got, want)
		}
	}
}
