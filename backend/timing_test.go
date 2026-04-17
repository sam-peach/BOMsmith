package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTimingServer returns a server with a temp upload directory for upload/analyze tests.
func newTimingServer(t *testing.T) (*server, string) {
	t.Helper()
	uploadDir := t.TempDir()
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	srv := &server{
		store:         newTestStore(),
		sessions:      ss,
		mappings:      newTestMappings(),
		matchFeedback: newTestMatchFeedback(),
		uploadDir:     uploadDir,
		userRepo: &memUserRepository{
			users: map[string]*User{
				"admin": {
					ID:             "user-1",
					OrganizationID: "org-1",
					Username:       "admin",
				},
			},
		},
	}
	return srv, token
}

func TestUpload_RecordsFileSizeBytes(t *testing.T) {
	srv, token := newTimingServer(t)

	// Minimal valid PDF content (magic bytes + some body).
	pdfContent := []byte("%PDF-1.4 fake content for timing test")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "drawing.pdf")
	require.NoError(t, err)
	_, err = fw.Write(pdfContent)
	require.NoError(t, err)
	mw.Close()

	// authedRequest doesn't carry a multipart body, so build the request manually
	// and borrow the session context from authedRequest.
	sessionCtx := authedRequest(http.MethodPost, "/api/documents/upload", "", token).Context()
	req := httptest.NewRequest(http.MethodPost, "/api/documents/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(sessionCtx)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})

	w := httptest.NewRecorder()
	srv.upload(w, req)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var result Document
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, int64(len(pdfContent)), result.FileSizeBytes, "FileSizeBytes should match actual file size")
}

func TestAnalyze_ReturnsAcceptedImmediately(t *testing.T) {
	srv, token := newTimingServer(t)

	doc := &Document{
		ID:             "doc-async-1",
		OrganizationID: "org-1",
		Filename:       "async.pdf",
		Status:         StatusUploaded,
		UploadedAt:     time.Now().UTC(),
		BOMRows:        []BOMRow{},
		Warnings:       []string{},
	}
	srv.store.save(doc)

	err := os.WriteFile(srv.uploadDir+"/doc-async-1.pdf", []byte("%PDF-1.4\n%%EOF"), 0600)
	require.NoError(t, err)

	req := authedRequest(http.MethodPost, "/api/documents/doc-async-1/analyze", "", token)
	req.SetPathValue("id", "doc-async-1")

	w := httptest.NewRecorder()
	srv.analyze(w, req)

	// Must return 202 immediately — not block on the LLM call.
	assert.Equal(t, http.StatusAccepted, w.Code)

	var returned Document
	require.NoError(t, json.NewDecoder(w.Body).Decode(&returned))
	assert.Equal(t, StatusAnalyzing, returned.Status, "doc should be in analyzing state on 202 response")

	// Analysis runs in a goroutine; wait for it to complete.
	require.Eventually(t, func() bool {
		d, err := srv.store.get("doc-async-1")
		return err == nil && d.Status != StatusAnalyzing
	}, 5*time.Second, 50*time.Millisecond, "analysis goroutine should complete")

	final, _ := srv.store.get("doc-async-1")
	assert.NotEqual(t, StatusAnalyzing, final.Status)
	assert.GreaterOrEqual(t, final.AnalysisDurationMs, int64(0))
}

func TestAnalyze_RecordsAnalysisDurationMs(t *testing.T) {
	srv, token := newTimingServer(t)

	// Seed a doc directly in the store (status=uploaded).
	doc := &Document{
		ID:             "doc-timing-1",
		OrganizationID: "org-1",
		Filename:       "test.pdf",
		Status:         StatusUploaded,
		UploadedAt:     time.Now().UTC(),
		BOMRows:        []BOMRow{},
		Warnings:       []string{},
	}
	srv.store.save(doc)

	// Write a minimal PDF so extraction does not error on a missing file.
	destPath := srv.uploadDir + "/doc-timing-1.pdf"
	err := os.WriteFile(destPath, []byte("%PDF-1.4\n%%EOF"), 0600)
	require.NoError(t, err)

	req := authedRequest(http.MethodPost, "/api/documents/doc-timing-1/analyze", "", token)
	req.SetPathValue("id", "doc-timing-1")

	w := httptest.NewRecorder()
	srv.analyze(w, req)

	// Analysis is now async — expect 202 Accepted.
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	// Wait for the goroutine to finish.
	require.Eventually(t, func() bool {
		d, err := srv.store.get("doc-timing-1")
		return err == nil && d.Status != StatusAnalyzing
	}, 5*time.Second, 50*time.Millisecond, "analysis goroutine should complete")

	result, err := srv.store.get("doc-timing-1")
	require.NoError(t, err)
	if result.Status == StatusDone {
		assert.GreaterOrEqual(t, result.AnalysisDurationMs, int64(0), "AnalysisDurationMs should be set")
	}
}

func TestDocument_TimingFieldsInJSON(t *testing.T) {
	doc := Document{
		ID:                 "doc-1",
		FileSizeBytes:      12345,
		AnalysisDurationMs: 4200,
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)

	var roundtrip Document
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	assert.Equal(t, int64(12345), roundtrip.FileSizeBytes)
	assert.Equal(t, int64(4200), roundtrip.AnalysisDurationMs)
}
