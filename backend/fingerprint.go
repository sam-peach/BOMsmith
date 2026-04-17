package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var standardRE = regexp.MustCompile(`(?i)\b(BS|UL|IEC|MIL|EN)\s*(\d+\w*)\b`)

// diameterRE matches e.g. "0.2mm", "0.35mm²", "0.5mm2", "16AWG".
// Group 1: numeric value. Group 2: unit.
// Trailing word boundary omitted because mm² ends with a non-word Unicode character.
var diameterRE = regexp.MustCompile(`(?i)\b(\d+\.?\d*)\s*(mm²|mm2|mm|awg)`)

var partTypeKeywords = []struct {
	keywords []string
	typ      string
}{
	{[]string{"wire", "cable", "conductor"}, "wire"},
	{[]string{"connector", "socket", "plug", "receptacle"}, "connector"},
	{[]string{"heatshrink", "heat shrink", "sleeving", "sleeve", "shrink"}, "heatshrink"},
	{[]string{"ferrule"}, "ferrule"},
	{[]string{"marker", "cable marker"}, "marker"},
	{[]string{"gland", "cable gland"}, "gland"},
	{[]string{"fuse"}, "fuse"},
	{[]string{"relay"}, "relay"},
	{[]string{"terminal"}, "terminal"},
	{[]string{"resistor"}, "resistor"},
	{[]string{"capacitor"}, "capacitor"},
	{[]string{"diode"}, "diode"},
}

var knownMaterials = []string{"pvc", "ptfe", "xlpe", "silicone", "nylon", "lszh", "rubber"}

var knownColors = []string{
	"blue", "red", "black", "white", "yellow", "green",
	"orange", "brown", "grey", "gray", "violet", "purple", "pink",
}

// buildFingerprint extracts structured attributes from a part description.
// All returned values are lowercase. Empty string means the attribute was
// not detected — a missing attribute is not the same as "doesn't have one".
func buildFingerprint(description string) PartFingerprint {
	if description == "" {
		return PartFingerprint{}
	}
	desc := strings.ToLower(description)
	var fp PartFingerprint

	// Type — first keyword match wins.
	for _, pt := range partTypeKeywords {
		for _, kw := range pt.keywords {
			if strings.Contains(desc, kw) {
				fp.Type = pt.typ
				goto doneType
			}
		}
	}
doneType:

	// Material.
	for _, m := range knownMaterials {
		if strings.Contains(desc, m) {
			fp.Material = m
			break
		}
	}

	// Standard (e.g. BS4808, BS 4808, UL1015, MIL-W-22759).
	if m := standardRE.FindStringSubmatch(description); m != nil {
		// Normalise: lowercase, remove internal spaces.
		fp.Standard = strings.ToLower(m[1]) + strings.ToLower(m[2])
	}

	// Diameter.
	if m := diameterRE.FindStringSubmatch(description); m != nil {
		num := m[1]
		unit := strings.ToLower(m[2])
		switch {
		case unit == "mm²" || unit == "mm2":
			fp.Diameter = normDiameter(num) + "mm²"
		case unit == "awg":
			fp.Diameter = strings.ToLower(num + unit)
		default:
			fp.Diameter = normDiameter(num) + "mm"
		}
	}

	// Color — first match wins.
	for _, c := range knownColors {
		if strings.Contains(desc, c) {
			fp.Color = c
			break
		}
	}

	return fp
}

// normDiameter parses a numeric string and returns it formatted to 2 decimal
// places, so "0.2" and "0.20" both become "0.20".
func normDiameter(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%.2f", f)
}
