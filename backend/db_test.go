package main

import (
	"os"
	"testing"
)

// TestMigrationsRun verifies that all migrations apply cleanly to an empty
// database. It is skipped when DATABASE_URL is not set so it never blocks
// local development without a Postgres instance.
func TestMigrationsRun(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping DB integration test")
	}

	db, err := openDB(url)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	// Running migrations twice must be idempotent.
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations (idempotent re-run): %v", err)
	}
}

func TestSeedAdmin(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping DB integration test")
	}

	db, err := openDB(url)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if err := seedAdmin(db, "Test Org", "testadmin", "testpass"); err != nil {
		t.Fatalf("seedAdmin: %v", err)
	}

	// Calling again when a user exists must be a no-op.
	if err := seedAdmin(db, "Test Org", "testadmin", "testpass"); err != nil {
		t.Fatalf("seedAdmin (second call): %v", err)
	}

	repo := &pgUserRepository{db: db}
	u, err := repo.findByUsername("testadmin")
	if err != nil {
		t.Fatalf("findByUsername: %v", err)
	}
	if u == nil {
		t.Fatal("expected admin user to exist after seed")
	}
	if u.OrganizationID == "" {
		t.Error("expected user to have an organization")
	}
}
