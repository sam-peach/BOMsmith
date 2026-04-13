package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ── pgUserRepository ──────────────────────────────────────────────────────────

type pgUserRepository struct {
	db *sql.DB
}

func (r *pgUserRepository) findByUsername(username string) (*User, error) {
	var u User
	err := r.db.QueryRow(
		`SELECT id, organization_id, username, password_hash, created_at, updated_at
		 FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.OrganizationID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("findByUsername: %w", err)
	}
	return &u, nil
}

func (r *pgUserRepository) findByID(id string) (*User, error) {
	var u User
	err := r.db.QueryRow(
		`SELECT id, organization_id, username, password_hash, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.OrganizationID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("findByID: %w", err)
	}
	return &u, nil
}

func (r *pgUserRepository) updatePassword(userID, newPasswordHash string) error {
	_, err := r.db.Exec(
		`UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`,
		newPasswordHash, userID,
	)
	if err != nil {
		return fmt.Errorf("updatePassword: %w", err)
	}
	return nil
}

func (r *pgUserRepository) createUser(orgID, username, passwordHash string) (*User, error) {
	var u User
	err := r.db.QueryRow(
		`INSERT INTO users (organization_id, username, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING id, organization_id, username, password_hash, created_at, updated_at`,
		orgID, username, passwordHash,
	).Scan(&u.ID, &u.OrganizationID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("createUser: %w", err)
	}
	return &u, nil
}

func (r *pgUserRepository) findOrgByID(orgID string) (*Organization, error) {
	var o Organization
	err := r.db.QueryRow(
		`SELECT id, name, created_at FROM organizations WHERE id = $1`, orgID,
	).Scan(&o.ID, &o.Name, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("findOrgByID: %w", err)
	}
	return &o, nil
}

// ── envUserRepository (dev-mode fallback, no DB required) ─────────────────────

// envUserRepository serves a single virtual user derived from environment
// variables. Used for local development when DATABASE_URL is not set.
type envUserRepository struct {
	username     string
	passwordHash string
	userID       string
	orgID        string
}

func newEnvUserRepository(username, password string) (*envUserRepository, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash admin password: %w", err)
	}
	return &envUserRepository{
		username:     username,
		passwordHash: string(hash),
		userID:       "dev-user",
		orgID:        "dev-org",
	}, nil
}

func (r *envUserRepository) findByID(id string) (*User, error) {
	if id == r.userID {
		return &User{
			ID:             r.userID,
			OrganizationID: r.orgID,
			Username:       r.username,
			PasswordHash:   r.passwordHash,
		}, nil
	}
	return nil, nil
}

func (r *envUserRepository) updatePassword(userID, newPasswordHash string) error {
	if userID == r.userID {
		r.passwordHash = newPasswordHash
	}
	return nil
}

func (r *envUserRepository) findByUsername(username string) (*User, error) {
	if strings.EqualFold(username, r.username) {
		return &User{
			ID:             r.userID,
			OrganizationID: r.orgID,
			Username:       r.username,
			PasswordHash:   r.passwordHash,
			CreatedAt:      time.Time{},
			UpdatedAt:      time.Time{},
		}, nil
	}
	return nil, nil
}

func (r *envUserRepository) createUser(_, _, _ string) (*User, error) {
	return nil, fmt.Errorf("user creation is not supported in dev mode (no DATABASE_URL set)")
}

func (r *envUserRepository) findOrgByID(orgID string) (*Organization, error) {
	return &Organization{ID: orgID, Name: "Dev Org"}, nil
}
