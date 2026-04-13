package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ── test double ───────────────────────────────────────────────────────────────

type memUserRepository struct {
	users map[string]*User
}

func (r *memUserRepository) findByUsername(username string) (*User, error) {
	if u, ok := r.users[username]; ok {
		return u, nil
	}
	return nil, nil
}

func (r *memUserRepository) findByID(id string) (*User, error) {
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, nil
}

func (r *memUserRepository) updatePassword(userID, newPasswordHash string) error {
	for _, u := range r.users {
		if u.ID == userID {
			u.PasswordHash = newPasswordHash
			return nil
		}
	}
	return nil
}

func (r *memUserRepository) createUser(orgID, username, passwordHash string) (*User, error) {
	u := &User{
		ID:             newID(),
		OrganizationID: orgID,
		Username:       username,
		PasswordHash:   passwordHash,
	}
	r.users[username] = u
	return u, nil
}

func (r *memUserRepository) findOrgByID(orgID string) (*Organization, error) {
	return &Organization{ID: orgID, Name: "Test Org"}, nil
}

func newAuthServer() *server {
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	return &server{
		store:    newStore(),
		sessions: newSessionStore(time.Hour),
		userRepo: &memUserRepository{
			users: map[string]*User{
				"admin": {
					ID:             "user-1",
					OrganizationID: "org-1",
					Username:       "admin",
					PasswordHash:   string(hash),
				},
			},
		},
	}
}

// ── sessionStore ──────────────────────────────────────────────────────────────

func TestSessionStore_CreateAndValid(t *testing.T) {
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	if !ss.valid(token) {
		t.Error("expected newly-created token to be valid")
	}
}

func TestSessionStore_UnknownTokenInvalid(t *testing.T) {
	ss := newSessionStore(time.Hour)
	if ss.valid("not-a-real-token") {
		t.Error("expected unknown token to be invalid")
	}
}

func TestSessionStore_ExpiredTokenInvalid(t *testing.T) {
	ss := newSessionStore(time.Millisecond)
	token := ss.create("user-1", "org-1")
	time.Sleep(10 * time.Millisecond)
	if ss.valid(token) {
		t.Error("expected expired token to be invalid")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	ss.delete(token)
	if ss.valid(token) {
		t.Error("expected deleted token to be invalid")
	}
}

func TestSessionStore_GetSession_ReturnsUserAndOrg(t *testing.T) {
	ss := newSessionStore(time.Hour)
	token := ss.create("user-42", "org-99")
	sd, ok := ss.getSession(token)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if sd.UserID != "user-42" {
		t.Errorf("UserID: got %q, want %q", sd.UserID, "user-42")
	}
	if sd.OrgID != "org-99" {
		t.Errorf("OrgID: got %q, want %q", sd.OrgID, "org-99")
	}
}

// ── login handler ─────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	srv := newAuthServer()
	body := `{"username":"admin","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.login(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			found = true
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
		}
	}
	if !found {
		t.Errorf("expected %q cookie in response", sessionCookieName)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	srv := newAuthServer()
	body := `{"username":"admin","password":"nope"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.login(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	srv := newAuthServer()
	body := `{"username":"nobody","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.login(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLogin_BadJSON(t *testing.T) {
	srv := newAuthServer()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.login(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── requireAuth middleware ────────────────────────────────────────────────────

func TestRequireAuth_NoCookie(t *testing.T) {
	srv := newAuthServer()
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/api/documents", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if called {
		t.Error("inner handler should not be called without valid cookie")
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	srv := newAuthServer()
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/api/documents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "bogus"})
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if called {
		t.Error("inner handler should not be called with invalid token")
	}
}

func TestRequireAuth_ValidToken_InjectsSession(t *testing.T) {
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	srv := &server{store: newStore(), sessions: ss}
	var gotSD *sessionData
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		gotSD = sessionFromContext(r)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/documents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()

	handler(w, req)

	if gotSD == nil {
		t.Fatal("expected session data in context")
	}
	if gotSD.UserID != "user-1" {
		t.Errorf("UserID: got %q, want %q", gotSD.UserID, "user-1")
	}
	if gotSD.OrgID != "org-1" {
		t.Errorf("OrgID: got %q, want %q", gotSD.OrgID, "org-1")
	}
}

// ── logout handler ────────────────────────────────────────────────────────────

func TestLogout_ClearsSession(t *testing.T) {
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	srv := &server{store: newStore(), sessions: ss}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()

	srv.logout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ss.valid(token) {
		t.Error("session should be invalidated after logout")
	}
}
