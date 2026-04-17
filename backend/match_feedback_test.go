package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── memMatchFeedbackRepository ────────────────────────────────────────────────

func TestMemMatchFeedback_RecordAccept(t *testing.T) {
	repo := newTestMatchFeedback()
	fb := &MatchFeedback{
		DrawingID:   "doc-1",
		CandidateID: "doc-2",
		Action:      "accept",
		Score:       0.72,
	}
	require.NoError(t, repo.record(fb, "org-1"))
	all := repo.all("org-1")
	require.Len(t, all, 1)
	assert.Equal(t, "accept", all[0].Action)
	assert.Equal(t, "doc-1", all[0].DrawingID)
	assert.Equal(t, "doc-2", all[0].CandidateID)
	assert.Equal(t, 0.72, all[0].Score)
	assert.NotEmpty(t, all[0].ID)
}

func TestMemMatchFeedback_RecordMultiple(t *testing.T) {
	repo := newTestMatchFeedback()
	for _, candidateID := range []string{"doc-2", "doc-3", "doc-4"} {
		_ = repo.record(&MatchFeedback{
			DrawingID:   "doc-1",
			CandidateID: candidateID,
			Action:      "reject",
			Score:       0.10,
		}, "org-1")
	}
	all := repo.all("org-1")
	assert.Len(t, all, 3)
}

func TestMemMatchFeedback_OrgIsolation(t *testing.T) {
	repo := newTestMatchFeedback()
	_ = repo.record(&MatchFeedback{DrawingID: "doc-1", CandidateID: "doc-2", Action: "accept", Score: 0.5}, "org-1")
	_ = repo.record(&MatchFeedback{DrawingID: "doc-3", CandidateID: "doc-4", Action: "accept", Score: 0.5}, "org-2")

	org1 := repo.all("org-1")
	org2 := repo.all("org-2")
	assert.Len(t, org1, 1)
	assert.Len(t, org2, 1)
	assert.Equal(t, "doc-1", org1[0].DrawingID)
	assert.Equal(t, "doc-3", org2[0].DrawingID)
}

func TestMemMatchFeedback_RecordStoresBreakdown(t *testing.T) {
	repo := newTestMatchFeedback()
	bd := &ScoreBreakdown{Filename: 0.3, CPN: 0.5, MPN: 0.0}
	_ = repo.record(&MatchFeedback{
		DrawingID:      "doc-1",
		CandidateID:    "doc-2",
		Action:         "accept",
		Score:          0.65,
		ScoreBreakdown: bd,
	}, "org-1")

	all := repo.all("org-1")
	require.Len(t, all, 1)
	require.NotNil(t, all[0].ScoreBreakdown)
	assert.InDelta(t, 0.3, all[0].ScoreBreakdown.Filename, 0.001)
	assert.InDelta(t, 0.5, all[0].ScoreBreakdown.CPN, 0.001)
}

// ── POST /api/match-feedback handler ─────────────────────────────────────────

func TestRecordFeedback_Accept(t *testing.T) {
	srv, token := newSettingsServer(t)
	// Seed source doc in store.
	srv.store.save(&Document{ID: "doc-src", OrganizationID: "org-1",
		Filename: "src.pdf", Status: StatusDone, BOMRows: []BOMRow{}, Warnings: []string{}})

	body := `[{"drawingId":"doc-1","candidateId":"doc-src","action":"accept","score":0.72}]`
	req := authedRequest(http.MethodPost, "/api/match-feedback", body, token)
	w := httptest.NewRecorder()

	srv.recordFeedback(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]int
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 1, resp["recorded"])
}

func TestRecordFeedback_RejectAll(t *testing.T) {
	srv, token := newSettingsServer(t)

	body := `[
		{"drawingId":"doc-1","candidateId":"doc-2","action":"reject","score":0.20},
		{"drawingId":"doc-1","candidateId":"doc-3","action":"reject","score":0.18}
	]`
	req := authedRequest(http.MethodPost, "/api/match-feedback", body, token)
	w := httptest.NewRecorder()

	srv.recordFeedback(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]int
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 2, resp["recorded"])
}

func TestRecordFeedback_InvalidAction(t *testing.T) {
	srv, token := newSettingsServer(t)

	body := `[{"drawingId":"doc-1","candidateId":"doc-2","action":"maybe","score":0.5}]`
	req := authedRequest(http.MethodPost, "/api/match-feedback", body, token)
	w := httptest.NewRecorder()

	srv.recordFeedback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRecordFeedback_EmptyArray(t *testing.T) {
	srv, token := newSettingsServer(t)
	req := authedRequest(http.MethodPost, "/api/match-feedback", "[]", token)
	w := httptest.NewRecorder()

	srv.recordFeedback(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecordFeedback_BadJSON(t *testing.T) {
	srv, token := newSettingsServer(t)
	req := authedRequest(http.MethodPost, "/api/match-feedback", "{bad", token)
	w := httptest.NewRecorder()

	srv.recordFeedback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── GET /api/documents/{id}/preview ──────────────────────────────────────────

func TestPreviewBOM_ReturnsLimitedRows(t *testing.T) {
	srv, token := newSettingsServer(t)
	rows := make([]BOMRow, 15)
	for i := range rows {
		rows[i] = BOMRow{ID: strings.Repeat("r", i+1), LineNumber: i + 1,
			Quantity: Quantity{Raw: "1", Flags: []string{}}, Flags: []string{}}
	}
	srv.store.save(&Document{
		ID: "doc-preview", OrganizationID: "org-1",
		Filename: "big.pdf", Status: StatusDone,
		BOMRows: rows, Warnings: []string{},
	})

	req := authedRequest(http.MethodGet, "/api/documents/doc-preview/preview", "", token)
	req.SetPathValue("id", "doc-preview")
	w := httptest.NewRecorder()

	srv.previewBOM(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Filename  string   `json:"filename"`
		Rows      []BOMRow `json:"rows"`
		TotalRows int      `json:"totalRows"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.LessOrEqual(t, len(resp.Rows), 10, "preview should be limited to 10 rows")
	assert.Equal(t, 15, resp.TotalRows, "totalRows should reflect full BOM")
	assert.Equal(t, "big.pdf", resp.Filename)
}

func TestPreviewBOM_NotFound(t *testing.T) {
	srv, token := newSettingsServer(t)
	req := authedRequest(http.MethodGet, "/api/documents/missing/preview", "", token)
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()

	srv.previewBOM(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPreviewBOM_SmallBOM(t *testing.T) {
	srv, token := newSettingsServer(t)
	srv.store.save(&Document{
		ID: "doc-small", OrganizationID: "org-1",
		Filename: "small.pdf", Status: StatusDone,
		BOMRows: []BOMRow{
			{ID: "r1", LineNumber: 1, Quantity: Quantity{Raw: "1", Flags: []string{}}, Flags: []string{}},
			{ID: "r2", LineNumber: 2, Quantity: Quantity{Raw: "2", Flags: []string{}}, Flags: []string{}},
		},
		Warnings: []string{},
	})

	req := authedRequest(http.MethodGet, "/api/documents/doc-small/preview", "", token)
	req.SetPathValue("id", "doc-small")
	w := httptest.NewRecorder()

	srv.previewBOM(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Rows      []BOMRow `json:"rows"`
		TotalRows int      `json:"totalRows"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Rows, 2, "all rows returned when BOM is smaller than limit")
	assert.Equal(t, 2, resp.TotalRows)
}
