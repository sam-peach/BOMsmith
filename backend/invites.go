package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Exported wrappers so test doubles can call the same primitives.
func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }
func hexEncodeToString(b []byte) string    { return hex.EncodeToString(b) }

const inviteTokenTTL = 7 * 24 * time.Hour

// ── interface ─────────────────────────────────────────────────────────────────

type inviteRepository interface {
	create(orgID, orgName, createdByUserID string, expiresAt time.Time) (*InviteToken, error)
	lookup(token string) (*InviteToken, bool)
	markUsed(token, usedByUserID string) error
}

// ── in-memory implementation (dev / tests) ────────────────────────────────────

type memInviteRepo struct {
	mu     sync.Mutex
	tokens map[string]*InviteToken
}

func newMemInviteRepo() *memInviteRepo {
	return &memInviteRepo{tokens: make(map[string]*InviteToken)}
}

func (r *memInviteRepo) create(orgID, orgName, createdByUserID string, expiresAt time.Time) (*InviteToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	inv := &InviteToken{
		ID:             newID(),
		OrganizationID: orgID,
		OrgName:        orgName,
		Token:          hex.EncodeToString(b),
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now().UTC(),
	}
	r.mu.Lock()
	r.tokens[inv.Token] = inv
	r.mu.Unlock()
	return inv, nil
}

func (r *memInviteRepo) lookup(token string) (*InviteToken, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.tokens[token]
	return inv, ok
}

func (r *memInviteRepo) markUsed(token, usedByUserID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv, ok := r.tokens[token]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	inv.UsedAt = &now
	inv.UsedByUserID = usedByUserID
	return nil
}

// ── Postgres implementation ───────────────────────────────────────────────────

type pgInviteRepository struct {
	db *sql.DB
}

func (r *pgInviteRepository) create(orgID, orgName, createdByUserID string, expiresAt time.Time) (*InviteToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	tok := hex.EncodeToString(b)

	inv := &InviteToken{OrgName: orgName}
	err := r.db.QueryRow(`
		INSERT INTO invite_tokens (organization_id, created_by, token, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, token, expires_at, created_at`,
		orgID, createdByUserID, tok, expiresAt,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.Token, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return inv, nil
}

func (r *pgInviteRepository) lookup(token string) (*InviteToken, bool) {
	var inv InviteToken
	var usedAt sql.NullTime
	err := r.db.QueryRow(`
		SELECT it.id, it.organization_id, o.name, it.token,
		       it.expires_at, it.used_at, it.created_at
		FROM invite_tokens it
		JOIN organizations o ON o.id = it.organization_id
		WHERE it.token = $1`,
		token,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.OrgName, &inv.Token,
		&inv.ExpiresAt, &usedAt, &inv.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		log.Printf("invite lookup error: %v", err)
		return nil, false
	}
	if usedAt.Valid {
		inv.UsedAt = &usedAt.Time
	}
	return &inv, true
}

func (r *pgInviteRepository) markUsed(token, usedByUserID string) error {
	_, err := r.db.Exec(`
		UPDATE invite_tokens SET used_at = now(), used_by = $1 WHERE token = $2`,
		usedByUserID, token,
	)
	return err
}

// ── handlers ──────────────────────────────────────────────────────────────────

// POST /api/invites — create a single-use invite link for the caller's org.
func (s *server) createInvite(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)

	// Look up the org name so the recipient knows what they're joining.
	orgName := "Your organisation"
	if org, err := s.userRepo.findOrgByID(sd.OrgID); err == nil && org != nil {
		orgName = org.Name
	}

	expiresAt := time.Now().UTC().Add(inviteTokenTTL)
	inv, err := s.invites.create(sd.OrgID, orgName, sd.UserID, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create invite")
		return
	}

	log.Printf("invite created: org=%s token=%.8s… expires=%s", sd.OrgID, inv.Token, inv.ExpiresAt.Format(time.RFC3339))
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     inv.Token,
		"expiresAt": inv.ExpiresAt,
		"inviteUrl": "/invite/" + inv.Token,
	})
}

// GET /api/invites/:token — validate a token; returns org name so the signup
// page can greet the recipient. Public endpoint (no auth required).
func (s *server) validateInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	inv, ok := s.invites.lookup(token)
	if !ok {
		writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if inv.UsedAt != nil {
		writeError(w, http.StatusGone, "invite has already been used")
		return
	}
	if time.Now().After(inv.ExpiresAt) {
		writeError(w, http.StatusGone, "invite has expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":   true,
		"orgName": inv.OrgName,
	})
}

// POST /api/invites/:token/accept — create a user account and sign them in.
// Public endpoint (no auth required).
func (s *server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	inv, ok := s.invites.lookup(token)
	if !ok {
		writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if inv.UsedAt != nil {
		writeError(w, http.StatusGone, "invite has already been used")
		return
	}
	if time.Now().After(inv.ExpiresAt) {
		writeError(w, http.StatusGone, "invite has expired")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	user, err := s.userRepo.createUser(inv.OrganizationID, req.Username, string(hash))
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	if err := s.invites.markUsed(token, user.ID); err != nil {
		log.Printf("markUsed error (non-fatal): %v", err)
	}

	sessionToken := s.sessions.create(user.ID, user.OrganizationID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(24 * time.Hour / time.Second),
	})

	log.Printf("invite accepted: user=%s org=%s", user.ID, user.OrganizationID)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}
