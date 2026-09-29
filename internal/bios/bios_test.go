package bios

import (
	"strings"
	"testing"
)

func TestVendorMatching(t *testing.T) {
	cases := map[string]string{
		"ASUSTeK COMPUTER INC.":              "ASUS",
		"Micro-Star International Co., Ltd.": "MSI",
		"Gigabyte Technology Co., Ltd.":      "Gigabyte",
		"ASRock":                             "ASRock",
		"Some Unknown Board Maker":           "Some Unknown Board Maker",
	}
	for maker, want := range cases {
		g := For(maker, "")
		if g.Vendor != want {
			t.Errorf("For(%q).Vendor = %q, want %q", maker, g.Vendor, want)
		}
		if len(g.Steps) < 3 || !strings.Contains(g.Steps[0], "BIOS") {
			t.Errorf("For(%q) has bad steps: %v", maker, g.Steps)
		}
	}
	if g := For("ASUSTeK COMPUTER INC.", "TUF GAMING B550-PLUS"); g.Vendor != "ASUS TUF GAMING B550-PLUS" {
		t.Errorf("product not appended: %q", g.Vendor)
	}
}
