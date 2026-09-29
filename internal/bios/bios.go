// Package bios turns a motherboard maker into step-by-step instructions for
// enabling Wake-on-LAN in the BIOS/UEFI.
//
// Windows cannot read BIOS settings on consumer gaming motherboards, so the
// server infers a BIOS problem from a failed wake test and shows these steps.
// Menu names differ between BIOS versions; the steps name the usual location
// and the setting names to look for.
package bios

import "strings"

// Guide is shown in the admin panel when a PC needs BIOS changes.
type Guide struct {
	Vendor string   `json:"vendor"`
	Key    string   `json:"key"`   // key to press during boot
	Steps  []string `json:"steps"` // what to change
	Note   string   `json:"note"`
}

type vendor struct {
	match []string
	guide Guide
}

var vendors = []vendor{
	{[]string{"asus"}, Guide{
		Vendor: "ASUS", Key: "Del (or F2)",
		Steps: []string{
			"Press F7 to switch to Advanced Mode if you see the EZ Mode screen.",
			"Go to Advanced → APM Configuration.",
			"Set \"Power On By PCI-E\" (older boards: \"Power On By PCI-E/PCI\") to Enabled.",
			"Set \"ErP Ready\" to Disabled.",
			"Press F10 to save and exit.",
		},
	}},
	{[]string{"micro-star", "msi"}, Guide{
		Vendor: "MSI", Key: "Del",
		Steps: []string{
			"Press F7 to switch to Advanced mode if needed.",
			"Go to Settings → Advanced → Wake Up Event Setup.",
			"Set \"Resume By PCI-E Device\" (older boards: \"Resume By PCI-E/Networking Device\") to Enabled.",
			"Go to Settings → Advanced → Power Management Setup and set \"ErP Ready\" to Disabled.",
			"Press F10 to save and exit.",
		},
	}},
	{[]string{"gigabyte"}, Guide{
		Vendor: "Gigabyte", Key: "Del",
		Steps: []string{
			"Press F2 to switch to Advanced Mode if needed.",
			"Go to Settings → Platform Power (older boards: Power Management).",
			"Set \"Wake on LAN\" (sometimes \"Resume by PME\" or \"PME Event Wake Up\") to Enabled.",
			"Set \"ErP\" to Disabled.",
			"Press F10 to save and exit.",
		},
	}},
	{[]string{"asrock"}, Guide{
		Vendor: "ASRock", Key: "F2 or Del",
		Steps: []string{
			"Press F6 to switch to Advanced Mode if needed.",
			"Go to Advanced → ACPI Configuration.",
			"Set \"PCIE Devices Power On\" (sometimes \"PCI Devices Power On\") to Enabled.",
			"Set \"Deep Sleep\" to Disabled (if present).",
			"Press F10 to save and exit.",
		},
	}},
	{[]string{"dell"}, Guide{
		Vendor: "Dell", Key: "F2",
		Steps: []string{
			"Go to Power Management → Wake on LAN/WLAN and choose \"LAN Only\".",
			"Go to Power Management → Deep Sleep Control and choose Disabled.",
			"Click Apply, then Exit.",
		},
	}},
	{[]string{"hewlett", "hp "}, Guide{
		Vendor: "HP", Key: "F10",
		Steps: []string{
			"Go to Advanced → Power Management Options (or Advanced → Device Power Management).",
			"Enable \"S5 Wake on LAN\" (or set \"Wake On LAN\" to \"Boot to Hard Drive\").",
			"Disable \"S5 Maximum Power Savings\" if present.",
			"Save changes and exit (F10).",
		},
	}},
	{[]string{"lenovo"}, Guide{
		Vendor: "Lenovo", Key: "F1 (or F2)",
		Steps: []string{
			"Go to Power → Automatic Power On and set \"Wake on LAN\" to Primary or Automatic.",
			"Go to Power and set \"Enhanced Power Saving Mode\" to Disabled.",
			"Press F10 to save and exit.",
		},
	}},
	{[]string{"biostar"}, Guide{
		Vendor: "Biostar", Key: "Del",
		Steps: []string{
			"Go to Advanced → ACPI Configuration (or Power Management).",
			"Set \"Wake on LAN\" / \"Power On by PCIE\" / \"PME Wake Up\" to Enabled.",
			"Set \"ErP\" / \"Deep S5\" to Disabled.",
			"Press F10 to save and exit.",
		},
	}},
}

var generic = Guide{
	Vendor: "", Key: "Del or F2",
	Steps: []string{
		"Look for a Power, APM, ACPI or Wake-up section (it may be under Advanced).",
		"Enable the setting called \"Wake on LAN\", \"Power On By PCI-E\", \"Resume by PME\" or \"PCIE Devices Power On\".",
		"Disable \"ErP\", \"EuP\" or \"Deep Sleep\" — these cut power to the network card when the PC is off.",
		"Save and exit (usually F10).",
	},
}

// For returns the guide for a motherboard maker as reported by Windows
// (Win32_BaseBoard.Manufacturer), e.g. "ASUSTeK COMPUTER INC.".
func For(maker, product string) Guide {
	m := strings.ToLower(maker + " ")
	var g Guide
	found := false
	for _, v := range vendors {
		for _, k := range v.match {
			if strings.Contains(m, k) {
				g, found = v.guide, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		g = generic
		g.Vendor = strings.TrimSpace(maker)
	}
	g.Steps = append([]string{"Restart the PC and press " + g.Key + " repeatedly while it starts, to open the BIOS."}, g.Steps...)
	g.Steps = append(g.Steps, "When Windows is back, click \"Test Wake-on-LAN\" again.")
	g.Note = "Menu names can differ slightly between BIOS versions. Also make sure the PC stays plugged in (power strip on) and connected by cable."
	if product != "" {
		g.Vendor = strings.TrimSpace(g.Vendor + " " + product)
	}
	return g
}
