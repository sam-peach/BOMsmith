package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildFingerprint_BlueWire(t *testing.T) {
	fp := buildFingerprint("Blue PVC BS4808 Wire, 0.2mm")
	assert.Equal(t, "wire", fp.Type)
	assert.Equal(t, "pvc", fp.Material)
	assert.Equal(t, "bs4808", fp.Standard)
	assert.Equal(t, "0.20mm", fp.Diameter)
	assert.Equal(t, "blue", fp.Color)
}

func TestBuildFingerprint_OrderIndependent(t *testing.T) {
	fp1 := buildFingerprint("Blue PVC BS4808 Wire, 0.2mm")
	fp2 := buildFingerprint("0.2mm PVC Wire to BS4808, Blue")
	assert.Equal(t, fp1, fp2)
}

func TestBuildFingerprint_CaseInsensitive(t *testing.T) {
	fp := buildFingerprint("BLUE PVC BS4808 WIRE 0.2MM")
	assert.Equal(t, "wire", fp.Type)
	assert.Equal(t, "blue", fp.Color)
	assert.Equal(t, "pvc", fp.Material)
}

func TestBuildFingerprint_StandardUL(t *testing.T) {
	fp := buildFingerprint("UL1015 Red PVC Wire 0.35mm")
	assert.Equal(t, "ul1015", fp.Standard)
	assert.Equal(t, "red", fp.Color)
	assert.Equal(t, "0.35mm", fp.Diameter)
}

func TestBuildFingerprint_DiameterCrossSection(t *testing.T) {
	fp := buildFingerprint("Red PVC Wire 0.5mm²")
	assert.Equal(t, "0.50mm²", fp.Diameter)
}

func TestBuildFingerprint_DiameterAWG(t *testing.T) {
	fp := buildFingerprint("UL1015 Red PVC Wire 16AWG")
	assert.Equal(t, "16awg", fp.Diameter)
}

func TestBuildFingerprint_DiameterNormalisation(t *testing.T) {
	// "0.2mm" and "0.20mm" must produce the same diameter string.
	fp1 := buildFingerprint("Wire 0.2mm")
	fp2 := buildFingerprint("Wire 0.20mm")
	assert.Equal(t, fp1.Diameter, fp2.Diameter)
	assert.Equal(t, "0.20mm", fp1.Diameter)
}

func TestBuildFingerprint_Connector(t *testing.T) {
	fp := buildFingerprint("SK1 6-way Connector")
	assert.Equal(t, "connector", fp.Type)
}

func TestBuildFingerprint_Heatshrink(t *testing.T) {
	fp := buildFingerprint("HS1 PVC Sleeving 3mm Black")
	assert.Equal(t, "heatshrink", fp.Type)
	assert.Equal(t, "pvc", fp.Material)
	assert.Equal(t, "black", fp.Color)
}

func TestBuildFingerprint_HeatshrinkKeyword(t *testing.T) {
	fp := buildFingerprint("Heatshrink tubing 6mm red")
	assert.Equal(t, "heatshrink", fp.Type)
}

func TestBuildFingerprint_Empty(t *testing.T) {
	fp := buildFingerprint("")
	assert.Equal(t, PartFingerprint{}, fp)
}

func TestBuildFingerprint_NoDiameter(t *testing.T) {
	fp := buildFingerprint("Blue PVC Wire")
	assert.Equal(t, "", fp.Diameter)
}

func TestBuildFingerprint_NoColor(t *testing.T) {
	fp := buildFingerprint("PVC Wire BS4808 0.5mm")
	assert.Equal(t, "", fp.Color)
}

func TestBuildFingerprint_StandardWithSpaces(t *testing.T) {
	fp := buildFingerprint("Wire to BS 4808 0.2mm")
	assert.Equal(t, "bs4808", fp.Standard)
}

func TestBuildFingerprint_PTFE(t *testing.T) {
	fp := buildFingerprint("PTFE insulated hook-up wire 0.5mm red")
	assert.Equal(t, "ptfe", fp.Material)
	assert.Equal(t, "wire", fp.Type)
}

func TestBuildFingerprint_CableSynonymForWire(t *testing.T) {
	fp := buildFingerprint("Red PVC Cable 0.35mm")
	assert.Equal(t, "wire", fp.Type)
}
