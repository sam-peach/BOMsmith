package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deleting a mapping by id removes it from the repository and 204s.
func TestDeleteMapping_RemovesEntry(t *testing.T) {
	srv, token := newSettingsServer(t)
	m := &Mapping{
		CustomerPartNumber: "DEL-001",
		InternalPartNumber: "I-DEL-001",
		ClientLabel:        "acme",
	}
	require.NoError(t, srv.mappings.save(m, "org-1"))
	require.NotEmpty(t, m.ID)

	req := authedRequest(http.MethodDelete, "/api/mappings/"+m.ID, "", token)
	req.SetPathValue("id", m.ID)
	w := httptest.NewRecorder()
	srv.deleteMapping(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	_, ok := srv.mappings.lookupClient("DEL-001", "org-1", "acme")
	assert.False(t, ok, "mapping must be gone after delete")
}

// Delete is org-scoped: an org cannot delete another org's mapping even if
// it guesses the id.
func TestDeleteMapping_OrgScoped(t *testing.T) {
	srv, token := newSettingsServer(t)
	other := &Mapping{
		CustomerPartNumber: "OTHER-001",
		InternalPartNumber: "I-OTHER",
	}
	require.NoError(t, srv.mappings.save(other, "org-OTHER"))

	req := authedRequest(http.MethodDelete, "/api/mappings/"+other.ID, "", token)
	req.SetPathValue("id", other.ID)
	w := httptest.NewRecorder()
	srv.deleteMapping(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"delete must not touch mappings owned by another org")

	_, ok := srv.mappings.lookupClient("OTHER-001", "org-OTHER", "")
	assert.True(t, ok, "the other org's mapping must still be present")
}

// Deleting a non-existent id returns 404 rather than silently succeeding.
func TestDeleteMapping_NotFound(t *testing.T) {
	srv, token := newSettingsServer(t)

	req := authedRequest(http.MethodDelete, "/api/mappings/does-not-exist", "", token)
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	srv.deleteMapping(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
