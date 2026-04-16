package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPersistenceServer(t *testing.T) (*server, string) {
	t.Helper()
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	srv := &server{
		store:         newStore(),
		sessions:      ss,
		mappings:      &inMemoryMappingRepository{store: &mappingStore{data: make(map[string]*Mapping), filePath: ""}},
		matchFeedback: newMemMatchFeedbackRepository(),
		userRepo: &memUserRepository{
			users: map[string]*User{
				"admin": {ID: "user-1", OrganizationID: "org-1", Username: "admin"},
			},
		},
	}
	return srv, token
}

func TestListDocuments_ReturnsAllOrgDocs(t *testing.T) {
	srv, token := newPersistenceServer(t)

	docs := []*Document{
		{ID: "d1", OrganizationID: "org-1", Filename: "a.pdf", Status: StatusDone, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{}},
		{ID: "d2", OrganizationID: "org-1", Filename: "b.pdf", Status: StatusAnalyzing, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{}},
		{ID: "d3", OrganizationID: "org-1", Filename: "c.pdf", Status: StatusError, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{}},
		{ID: "dx", OrganizationID: "org-2", Filename: "other.pdf", Status: StatusDone, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{}},
	}
	for _, d := range docs {
		srv.store.save(d)
	}

	req := authedRequest(http.MethodGet, "/api/documents", "", token)
	w := httptest.NewRecorder()
	srv.listDocuments(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var result []Document
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))

	// Only org-1 docs returned, all statuses included.
	assert.Len(t, result, 3)
	ids := make([]string, len(result))
	for i, d := range result {
		ids[i] = d.ID
	}
	assert.ElementsMatch(t, []string{"d1", "d2", "d3"}, ids)
}

func TestDeleteDocument_RemovesDoc(t *testing.T) {
	srv, token := newPersistenceServer(t)

	doc := &Document{
		ID: "del-1", OrganizationID: "org-1", Filename: "x.pdf",
		Status: StatusDone, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{},
	}
	srv.store.save(doc)

	req := authedRequest(http.MethodDelete, "/api/documents/del-1", "", token)
	req.SetPathValue("id", "del-1")
	w := httptest.NewRecorder()
	srv.deleteDocument(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	// Doc should no longer be retrievable.
	_, err := srv.store.get("del-1")
	assert.Error(t, err)
}

func TestDeleteDocument_NotFound(t *testing.T) {
	srv, token := newPersistenceServer(t)

	req := authedRequest(http.MethodDelete, "/api/documents/ghost", "", token)
	req.SetPathValue("id", "ghost")
	w := httptest.NewRecorder()
	srv.deleteDocument(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestResetAnalyzing_SetsErrorOnStaleDoc(t *testing.T) {
	store := newStore()

	store.save(&Document{
		ID: "stale-1", OrganizationID: "org-1", Filename: "s.pdf",
		Status: StatusAnalyzing, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{},
	})
	store.save(&Document{
		ID: "done-1", OrganizationID: "org-1", Filename: "d.pdf",
		Status: StatusDone, UploadedAt: time.Now().UTC(), BOMRows: []BOMRow{}, Warnings: []string{},
	})

	err := store.resetAnalyzing()
	require.NoError(t, err)

	stale, _ := store.get("stale-1")
	assert.Equal(t, StatusError, stale.Status)
	assert.NotEmpty(t, stale.ErrorMessage)

	done, _ := store.get("done-1")
	assert.Equal(t, StatusDone, done.Status) // untouched
}
