package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMappings_SaveAndLookup(t *testing.T) {
	ms := newTestMappings()

	m := &Mapping{
		CustomerPartNumber:     "CUST-001",
		InternalPartNumber:     "SC-001",
		ManufacturerPartNumber: "MPN-001",
		Description:            "Test part",
		Source:                 "manual",
	}
	require.NoError(t, ms.save(m, "org-1"))

	got, ok := ms.lookup("CUST-001", "org-1")
	require.True(t, ok)
	assert.Equal(t, "SC-001", got.InternalPartNumber)
	assert.Equal(t, "MPN-001", got.ManufacturerPartNumber)
}

func TestMappings_LookupCaseInsensitive(t *testing.T) {
	ms := newTestMappings()
	require.NoError(t, ms.save(&Mapping{CustomerPartNumber: "cust-001", InternalPartNumber: "SC-001"}, "org-1"))

	_, ok := ms.lookup("CUST-001", "org-1")
	assert.True(t, ok)

	_, ok = ms.lookup("Cust-001", "org-1")
	assert.True(t, ok)
}

func TestMappings_LookupMiss(t *testing.T) {
	ms := newTestMappings()
	_, ok := ms.lookup("MISSING", "org-1")
	assert.False(t, ok)
}

func TestMappings_LookupEmptyKey(t *testing.T) {
	ms := newTestMappings()
	_, ok := ms.lookup("", "org-1")
	assert.False(t, ok)
}

func TestMappings_SaveAssignsID(t *testing.T) {
	ms := newTestMappings()
	m := &Mapping{CustomerPartNumber: "CUST-001"}
	require.NoError(t, ms.save(m, "org-1"))
	assert.NotEmpty(t, m.ID)
}

func TestMappings_SaveSetsTimestamps(t *testing.T) {
	before := time.Now()
	ms := newTestMappings()
	m := &Mapping{CustomerPartNumber: "CUST-001"}
	require.NoError(t, ms.save(m, "org-1"))
	assert.True(t, m.CreatedAt.After(before) || m.CreatedAt.Equal(before))
	assert.True(t, m.UpdatedAt.After(before) || m.UpdatedAt.Equal(before))
}

func TestMappings_UpdatePreservesCreatedAt(t *testing.T) {
	ms := newTestMappings()
	m := &Mapping{CustomerPartNumber: "CUST-001", InternalPartNumber: "SC-001"}
	require.NoError(t, ms.save(m, "org-1"))
	created := m.CreatedAt

	m2 := &Mapping{CustomerPartNumber: "CUST-001", InternalPartNumber: "SC-002"}
	require.NoError(t, ms.save(m2, "org-1"))

	got, _ := ms.lookup("CUST-001", "org-1")
	assert.Equal(t, created, got.CreatedAt, "CreatedAt must not change on update")
	assert.Equal(t, "SC-002", got.InternalPartNumber)
}

func TestMappings_SaveRequiresCustomerPartNumber(t *testing.T) {
	ms := newTestMappings()
	err := ms.save(&Mapping{CustomerPartNumber: ""}, "org-1")
	assert.Error(t, err)
}

func TestMappings_All(t *testing.T) {
	ms := newTestMappings()
	require.NoError(t, ms.save(&Mapping{CustomerPartNumber: "CUST-001"}, "org-1"))
	require.NoError(t, ms.save(&Mapping{CustomerPartNumber: "CUST-002"}, "org-1"))
	assert.Len(t, ms.all("org-1"), 2)
}
