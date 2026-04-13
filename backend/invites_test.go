package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── test doubles ──────────────────────────────────────────────────────────────

type memInviteRepository struct {
	tokens map[string]*InviteToken
}

func (r *memInviteRepository) create(orgID, orgName, createdByUserID string, expiresAt time.Time) (*InviteToken, error) {
	b := make([]byte, 16)
	if _, err := cryptoRandRead(b); err != nil {
		return nil, err
	}
	tok := hexEncodeToString(b)
	inv := &InviteToken{
		ID:             newID(),
		OrganizationID: orgID,
		OrgName:        orgName,
		Token:          tok,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now().UTC(),
	}
	r.tokens[tok] = inv
	return inv, nil
}

func (r *memInviteRepository) lookup(token string) (*InviteToken, bool) {
	inv, ok := r.tokens[token]
	return inv, ok
}

func (r *memInviteRepository) markUsed(token, usedByUserID string) error {
	inv, ok := r.tokens[token]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	inv.UsedAt = &now
	inv.UsedByUserID = usedByUserID
	return nil
}

func newInviteServer(t *testing.T) (*server, string) {
	t.Helper()
	srv, token := newSettingsServer(t)
	srv.invites = &memInviteRepository{tokens: make(map[string]*InviteToken)}
	return srv, token
}

// ── createInvite ──────────────────────────────────────────────────────────────

func TestCreateInvite_ReturnsToken(t *testing.T) {
	srv, token := newInviteServer(t)
	req := authedRequest(http.MethodPost, "/api/invites", "", token)
	w := httptest.NewRecorder()

	srv.createInvite(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
		InviteURL string `json:"inviteUrl"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if resp.InviteURL == "" {
		t.Error("expected non-empty inviteUrl")
	}
	if !strings.Contains(resp.InviteURL, resp.Token) {
		t.Errorf("inviteUrl %q should contain token %q", resp.InviteURL, resp.Token)
	}
}

func TestCreateInvite_ExpiresInFuture(t *testing.T) {
	srv, token := newInviteServer(t)
	req := authedRequest(http.MethodPost, "/api/invites", "", token)
	w := httptest.NewRecorder()

	srv.createInvite(w, req)

	var resp struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.ExpiresAt.After(time.Now()) {
		t.Error("expiresAt should be in the future")
	}
}

// ── validateInvite ────────────────────────────────────────────────────────────

func TestValidateInvite_ValidToken(t *testing.T) {
	srv, authToken := newInviteServer(t)

	// Create invite first.
	req := authedRequest(http.MethodPost, "/api/invites", "", authToken)
	w := httptest.NewRecorder()
	srv.createInvite(w, req)
	var created struct{ Token string `json:"token"` }
	json.NewDecoder(w.Body).Decode(&created)

	// Validate it.
	req2 := httptest.NewRequest(http.MethodGet, "/api/invites/"+created.Token, nil)
	req2.SetPathValue("token", created.Token)
	w2 := httptest.NewRecorder()
	srv.validateInvite(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp struct {
		Valid   bool   `json:"valid"`
		OrgName string `json:"orgName"`
	}
	json.NewDecoder(w2.Body).Decode(&resp)
	if !resp.Valid {
		t.Error("expected valid:true")
	}
}

func TestValidateInvite_UnknownToken(t *testing.T) {
	srv, _ := newInviteServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/invites/no-such-token", nil)
	req.SetPathValue("token", "no-such-token")
	w := httptest.NewRecorder()

	srv.validateInvite(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestValidateInvite_ExpiredToken(t *testing.T) {
	srv, _ := newInviteServer(t)
	// Manually insert an already-expired token.
	past := time.Now().Add(-time.Hour)
	srv.invites.(*memInviteRepository).tokens["expired-tok"] = &InviteToken{
		ID:             "inv-1",
		OrganizationID: "org-1",
		OrgName:        "Test Org",
		Token:          "expired-tok",
		ExpiresAt:      past,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/invites/expired-tok", nil)
	req.SetPathValue("token", "expired-tok")
	w := httptest.NewRecorder()

	srv.validateInvite(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for expired token, got %d", w.Code)
	}
}

func TestValidateInvite_UsedToken(t *testing.T) {
	srv, _ := newInviteServer(t)
	now := time.Now().UTC()
	srv.invites.(*memInviteRepository).tokens["used-tok"] = &InviteToken{
		ID:             "inv-2",
		OrganizationID: "org-1",
		OrgName:        "Test Org",
		Token:          "used-tok",
		ExpiresAt:      time.Now().Add(7 * 24 * time.Hour),
		UsedAt:         &now,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/invites/used-tok", nil)
	req.SetPathValue("token", "used-tok")
	w := httptest.NewRecorder()

	srv.validateInvite(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for used token, got %d", w.Code)
	}
}

// ── acceptInvite ──────────────────────────────────────────────────────────────

func TestAcceptInvite_CreatesUserAndSession(t *testing.T) {
	srv, authToken := newInviteServer(t)

	// Create invite.
	req := authedRequest(http.MethodPost, "/api/invites", "", authToken)
	w := httptest.NewRecorder()
	srv.createInvite(w, req)
	var created struct{ Token string `json:"token"` }
	json.NewDecoder(w.Body).Decode(&created)

	// Accept it.
	body := `{"username":"newuser","password":"pass1234"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/invites/"+created.Token+"/accept", strings.NewReader(body))
	req2.SetPathValue("token", created.Token)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	srv.acceptInvite(w2, req2)

	if w2.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w2.Code, w2.Body.String())
	}
	// Should set a session cookie.
	var hasCookie bool
	for _, c := range w2.Result().Cookies() {
		if c.Name == sessionCookieName {
			hasCookie = true
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
		}
	}
	if !hasCookie {
		t.Error("expected session cookie after accepting invite")
	}
	// User should exist in the repo.
	u, _ := srv.userRepo.findByUsername("newuser")
	if u == nil {
		t.Fatal("expected user to be created in repository")
	}
	if u.OrganizationID != "org-1" {
		t.Errorf("expected user in org-1, got %q", u.OrganizationID)
	}
}

func TestAcceptInvite_TokenMarkedUsed(t *testing.T) {
	srv, authToken := newInviteServer(t)

	req := authedRequest(http.MethodPost, "/api/invites", "", authToken)
	w := httptest.NewRecorder()
	srv.createInvite(w, req)
	var created struct{ Token string `json:"token"` }
	json.NewDecoder(w.Body).Decode(&created)

	body := `{"username":"alice","password":"pass1234"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/invites/"+created.Token+"/accept", strings.NewReader(body))
	req2.SetPathValue("token", created.Token)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.acceptInvite(w2, req2)

	inv := srv.invites.(*memInviteRepository).tokens[created.Token]
	if inv.UsedAt == nil {
		t.Error("expected token to be marked used after accept")
	}
}

func TestAcceptInvite_CannotReuseToken(t *testing.T) {
	srv, authToken := newInviteServer(t)

	req := authedRequest(http.MethodPost, "/api/invites", "", authToken)
	w := httptest.NewRecorder()
	srv.createInvite(w, req)
	var created struct{ Token string `json:"token"` }
	json.NewDecoder(w.Body).Decode(&created)

	// First use.
	body := `{"username":"user1","password":"pass1234"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/invites/"+created.Token+"/accept", strings.NewReader(body))
	req2.SetPathValue("token", created.Token)
	req2.Header.Set("Content-Type", "application/json")
	srv.acceptInvite(httptest.NewRecorder(), req2)

	// Second use — should fail.
	body2 := `{"username":"user2","password":"pass1234"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/invites/"+created.Token+"/accept", strings.NewReader(body2))
	req3.SetPathValue("token", created.Token)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	srv.acceptInvite(w3, req3)

	if w3.Code != http.StatusGone {
		t.Fatalf("expected 410 on reuse, got %d: %s", w3.Code, w3.Body.String())
	}
}

func TestAcceptInvite_ExpiredToken(t *testing.T) {
	srv, _ := newInviteServer(t)
	srv.invites.(*memInviteRepository).tokens["exp-tok"] = &InviteToken{
		ID: "inv-3", OrganizationID: "org-1", OrgName: "Test Org",
		Token: "exp-tok", ExpiresAt: time.Now().Add(-time.Hour),
	}

	body := `{"username":"late","password":"pass1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/invites/exp-tok/accept", strings.NewReader(body))
	req.SetPathValue("token", "exp-tok")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.acceptInvite(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestAcceptInvite_MissingFields(t *testing.T) {
	srv, authToken := newInviteServer(t)
	req := authedRequest(http.MethodPost, "/api/invites", "", authToken)
	w := httptest.NewRecorder()
	srv.createInvite(w, req)
	var created struct{ Token string `json:"token"` }
	json.NewDecoder(w.Body).Decode(&created)

	body := `{"username":"","password":""}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/invites/"+created.Token+"/accept", strings.NewReader(body))
	req2.SetPathValue("token", created.Token)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	srv.acceptInvite(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w2.Code)
	}
}
