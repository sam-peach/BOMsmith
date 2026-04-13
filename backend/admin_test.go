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

// newAdminServer returns a test server wired with an in-memory error log
// and adminUsername set to "admin" (matching the user seeded by newSettingsServer).
func newAdminServer(t *testing.T) (*server, string) {
	t.Helper()
	srv, token := newSettingsServer(t)
	srv.errorLog      = &memErrorLogRepository{}
	srv.adminUsername = "admin"
	return srv, token
}

// ----------------------------------------------------------------------------
// requireAdmin middleware
// ----------------------------------------------------------------------------

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	srv, token := newAdminServer(t)
	reached := false
	handler := srv.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := authedRequest(http.MethodGet, "/api/admin/errors", "", token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.True(t, reached, "admin handler should have been called")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdmin_BlocksNonAdmin(t *testing.T) {
	srv, token := newAdminServer(t)
	srv.adminUsername = "someone-else" // logged-in user is "admin", not this
	reached := false
	handler := srv.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	req := authedRequest(http.MethodGet, "/api/admin/errors", "", token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.False(t, reached, "non-admin should not reach handler")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ----------------------------------------------------------------------------
// memErrorLogRepository
// ----------------------------------------------------------------------------

func TestMemErrorLog_AppendAndRecent(t *testing.T) {
	repo := &memErrorLogRepository{}
	_ = repo.append(&ErrorLogEntry{
		Timestamp: time.Now(),
		Level:     "error",
		Component: "analysis",
		Message:   "something went wrong",
		DocName:   "drawing.pdf",
	})

	entries, err := repo.recent(10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "error", entries[0].Level)
	assert.Equal(t, "analysis", entries[0].Component)
	assert.Equal(t, "something went wrong", entries[0].Message)
}

func TestMemErrorLog_RecentLimitRespected(t *testing.T) {
	repo := &memErrorLogRepository{}
	for i := 0; i < 20; i++ {
		_ = repo.append(&ErrorLogEntry{Level: "error", Component: "analysis", Message: "err"})
	}

	entries, err := repo.recent(5)
	require.NoError(t, err)
	assert.Len(t, entries, 5)
}

func TestMemErrorLog_RecentNewestFirst(t *testing.T) {
	repo := &memErrorLogRepository{}
	_ = repo.append(&ErrorLogEntry{Level: "error", Message: "first",  Timestamp: time.Now().Add(-2 * time.Second)})
	_ = repo.append(&ErrorLogEntry{Level: "error", Message: "second", Timestamp: time.Now().Add(-1 * time.Second)})
	_ = repo.append(&ErrorLogEntry{Level: "error", Message: "third",  Timestamp: time.Now()})

	entries, err := repo.recent(10)
	require.NoError(t, err)
	assert.Equal(t, "third",  entries[0].Message)
	assert.Equal(t, "second", entries[1].Message)
	assert.Equal(t, "first",  entries[2].Message)
}

func TestMemErrorLog_EmptyReturnsEmptySlice(t *testing.T) {
	repo := &memErrorLogRepository{}
	entries, err := repo.recent(10)
	require.NoError(t, err)
	assert.NotNil(t, entries)
	assert.Empty(t, entries)
}

// ----------------------------------------------------------------------------
// GET /api/admin/errors
// ----------------------------------------------------------------------------

func TestListErrors_ReturnsEntries(t *testing.T) {
	srv, token := newAdminServer(t)
	_ = srv.errorLog.append(&ErrorLogEntry{
		Timestamp: time.Now(),
		Level:     "error",
		Component: "analysis",
		Message:   "LLM timeout",
		DocName:   "harness.pdf",
	})

	req := authedRequest(http.MethodGet, "/api/admin/errors", "", token)
	w := httptest.NewRecorder()
	srv.requireAdmin(http.HandlerFunc(srv.listErrors)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var entries []*ErrorLogEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "LLM timeout", entries[0].Message)
}

func TestListErrors_EmptyWhenNoErrors(t *testing.T) {
	srv, token := newAdminServer(t)
	req := authedRequest(http.MethodGet, "/api/admin/errors", "", token)
	w := httptest.NewRecorder()
	srv.requireAdmin(http.HandlerFunc(srv.listErrors)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var entries []*ErrorLogEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&entries))
	assert.Empty(t, entries)
}

// ----------------------------------------------------------------------------
// GET /api/auth/me — isAdmin field
// ----------------------------------------------------------------------------

func TestAuthMe_IsAdminTrue(t *testing.T) {
	srv, token := newAdminServer(t)
	req := authedRequest(http.MethodGet, "/api/auth/me", "", token)
	w := httptest.NewRecorder()
	srv.authMe(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, true, body["isAdmin"])
}

func TestAuthMe_IsAdminFalse(t *testing.T) {
	srv, token := newAdminServer(t)
	srv.adminUsername = "someone-else"
	req := authedRequest(http.MethodGet, "/api/auth/me", "", token)
	w := httptest.NewRecorder()
	srv.authMe(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, false, body["isAdmin"])
}
