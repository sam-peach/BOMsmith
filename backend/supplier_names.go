package main

import (
	"slices"
	"strings"
)

// normaliseSupplierName collapses distributors' verbose display names to the
// short forms used elsewhere in the codebase ("DigiKey", "Farnell" etc.).
// Brand equivalents are merged where they map to the same parent company:
// Premier Farnell sells under Farnell/Newark/element14, Arrow owns Verical,
// and "RS (Formerly Allied Electronics)" is the same RS we already know.
// Unknown names pass through trimmed but otherwise unchanged.
//
// Shared by every pricing provider so the same vendor reported under
// different geo/legal names collapses to one row in the UI.
func normaliseSupplierName(s string) string {
	clean := strings.ToLower(strings.TrimSpace(s))
	switch {
	case matchesAny(clean, "digi-key", "digi-key electronics", "digikey"):
		return "DigiKey"
	case matchesAny(clean, "mouser", "mouser electronics"):
		return "Mouser"
	case matchesAny(clean, "farnell", "element14", "element 14", "farnell element14", "newark") ||
		strings.HasPrefix(clean, "element14 ") || strings.HasPrefix(clean, "element 14 "):
		return "Farnell"
	case matchesAny(clean, "rs", "rs components", "rs pro") ||
		strings.HasPrefix(clean, "rs ("):
		return "RS"
	case matchesAny(clean, "arrow", "arrow electronics", "verical"):
		return "Arrow"
	case matchesAny(clean, "avnet", "avnet abacus", "avnet silica"):
		return "Avnet"
	case matchesAny(clean, "future", "future electronics"):
		return "Future"
	case matchesAny(clean, "tme", "tme electronic components"):
		return "TME"
	case matchesAny(clean, "lcsc", "lcsc electronics"):
		return "LCSC"
	case matchesAny(clean, "conrad", "conrad electronic"):
		return "Conrad"
	default:
		return strings.TrimSpace(s)
	}
}

func matchesAny(s string, options ...string) bool {
	return slices.Contains(options, s)
}
