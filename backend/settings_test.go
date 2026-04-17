package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// newSettingsServer returns a server with a single "admin" user (id "user-1")
// pre-seeded, and a valid session cookie token for that user.
func newSettingsServer(t *testing.T) (*server, string) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpass"), bcrypt.MinCost)
	ss := newSessionStore(time.Hour)
	token := ss.create("user-1", "org-1")
	srv := &server{
		store:         newTestStore(),
		sessions:      ss,
		mappings:      newTestMappings(),
		matchFeedback: newTestMatchFeedback(),
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
	return srv, token
}

func authedRequest(method, path, body, token string) *http.Request {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})

	// Inject session data into context (simulates requireAuth middleware).
	ctx := context.WithValue(req.Context(), sessionCtxKey, &sessionData{
		UserID: "user-1",
		OrgID:  "org-1",
		expiry: time.Now().Add(time.Hour),
	})
	return req.WithContext(ctx)
}

func TestChangePassword_Success(t *testing.T) {
	srv, token := newSettingsServer(t)
	body := `{"currentPassword":"oldpass","newPassword":"newpass123"}`
	req := authedRequest(http.MethodPut, "/api/users/me/password", body, token)
	w := httptest.NewRecorder()

	srv.changePassword(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the stored hash was updated.
	u, _ := srv.userRepo.findByUsername("admin")
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("newpass123")); err != nil {
		t.Error("stored hash should match new password after change")
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	srv, token := newSettingsServer(t)
	body := `{"currentPassword":"wrongpass","newPassword":"newpass123"}`
	req := authedRequest(http.MethodPut, "/api/users/me/password", body, token)
	w := httptest.NewRecorder()

	srv.changePassword(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestChangePassword_EmptyNewPassword(t *testing.T) {
	srv, token := newSettingsServer(t)
	body := `{"currentPassword":"oldpass","newPassword":""}`
	req := authedRequest(http.MethodPut, "/api/users/me/password", body, token)
	w := httptest.NewRecorder()

	srv.changePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_MissingFields(t *testing.T) {
	srv, token := newSettingsServer(t)
	body := `{}`
	req := authedRequest(http.MethodPut, "/api/users/me/password", body, token)
	w := httptest.NewRecorder()

	srv.changePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_BadJSON(t *testing.T) {
	srv, token := newSettingsServer(t)
	req := authedRequest(http.MethodPut, "/api/users/me/password", "{bad", token)
	w := httptest.NewRecorder()

	srv.changePassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
