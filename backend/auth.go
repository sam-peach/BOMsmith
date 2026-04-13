package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "sme_session"

// ── session store ─────────────────────────────────────────────────────────────

type sessionData struct {
	UserID string
	OrgID  string
	expiry time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]sessionData
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{
		sessions: make(map[string]sessionData),
		ttl:      ttl,
	}
}

func (ss *sessionStore) create(userID, orgID string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	token := hex.EncodeToString(b)
	ss.mu.Lock()
	ss.sessions[token] = sessionData{
		UserID: userID,
		OrgID:  orgID,
		expiry: time.Now().Add(ss.ttl),
	}
	ss.mu.Unlock()
	return token
}

func (ss *sessionStore) getSession(token string) (sessionData, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	sd, ok := ss.sessions[token]
	if !ok {
		return sessionData{}, false
	}
	if time.Now().After(sd.expiry) {
		delete(ss.sessions, token)
		return sessionData{}, false
	}
	return sd, true
}

func (ss *sessionStore) valid(token string) bool {
	_, ok := ss.getSession(token)
	return ok
}

func (ss *sessionStore) delete(token string) {
	ss.mu.Lock()
	delete(ss.sessions, token)
	ss.mu.Unlock()
}

// ── context helpers ───────────────────────────────────────────────────────────

type contextKey string

const sessionCtxKey contextKey = "session"

// sessionFromContext retrieves the session data injected by requireAuth.
func sessionFromContext(r *http.Request) *sessionData {
	sd, _ := r.Context().Value(sessionCtxKey).(*sessionData)
	return sd
}

// ── userRepository interface ──────────────────────────────────────────────────

type userRepository interface {
	findByUsername(username string) (*User, error)
	findByID(id string) (*User, error)
	createUser(orgID, username, passwordHash string) (*User, error)
	updatePassword(userID, newPasswordHash string) error
	findOrgByID(orgID string) (*Organization, error)
}

// ── middleware ────────────────────────────────────────────────────────────────

// requireAuth wraps a handler, returning 401 if no valid session cookie is
// present. On success it injects the sessionData into the request context so
// handlers can call sessionFromContext(r).
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sd, ok := s.sessions.getSession(cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), sessionCtxKey, &sd)
		next(w, r.WithContext(ctx))
	}
}

// ── auth handlers ─────────────────────────────────────────────────────────────

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// POST /api/auth/login
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := s.userRepo.findByUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token := s.sessions.create(user.ID, user.OrganizationID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(24 * time.Hour / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/auth/logout
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/auth/me — reached only when requireAuth has already validated the session.
func (s *server) authMe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
