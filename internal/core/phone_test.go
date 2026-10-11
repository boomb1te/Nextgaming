package core

import "testing"

func TestNormalizeTMPhone(t *testing.T) {
	cases := []struct {
		in    string
		canon string
		key   string
		ok    bool
	}{
		{"+99365123456", "+99365123456", "65123456", true},
		{"+993 65 12 34 56", "+99365123456", "65123456", true},
		{"65123456", "+99365123456", "65123456", true},
		{"065123456", "+99365123456", "65123456", true},     // leading trunk 0
		{"8 65 12 34 56", "+99365123456", "65123456", true}, // old trunk 8
		{"99365123456", "+99365123456", "65123456", true},
		{" (+993) 65-12-34-56 ", "+99365123456", "65123456", true},
		{"123", "", "", false},     // too short
		{"", "", "", false},        // empty
		{"abcd", "", "", false},    // no digits
		{"1234567", "", "", false}, // 7 digits
	}
	for _, c := range cases {
		canon, key, ok := NormalizeTMPhone(c.in)
		if ok != c.ok || canon != c.canon || key != c.key {
			t.Errorf("NormalizeTMPhone(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, canon, key, ok, c.canon, c.key, c.ok)
		}
	}
	// Two different spellings of the same number must share a key.
	if PhoneKey("+993 65 12 34 56") != PhoneKey("065123456") {
		t.Fatal("different spellings of one number produced different keys")
	}
}
