package main

import (
	"database/sql"
	"sort"
	"sync"
)

// errorLogRepository stores structured error and warning entries.
type errorLogRepository interface {
	append(entry *ErrorLogEntry) error
	recent(n int) ([]*ErrorLogEntry, error)
}

// memErrorLogRepository is an in-memory store capped at maxErrorLogEntries.
const maxErrorLogEntries = 500

type memErrorLogRepository struct {
	mu      sync.Mutex
	entries []*ErrorLogEntry
}

func (r *memErrorLogRepository) append(entry *ErrorLogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	if len(r.entries) > maxErrorLogEntries {
		r.entries = r.entries[len(r.entries)-maxErrorLogEntries:]
	}
	return nil
}

func (r *memErrorLogRepository) recent(n int) ([]*ErrorLogEntry, error) {
	r.mu.Lock()
	out := make([]*ErrorLogEntry, len(r.entries))
	copy(out, r.entries)
	r.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out, nil
}

// pgErrorLogRepository persists entries in the error_log table.
type pgErrorLogRepository struct {
	db *sql.DB
}

func (r *pgErrorLogRepository) append(entry *ErrorLogEntry) error {
	_, err := r.db.Exec(`
		INSERT INTO error_log (timestamp, level, component, message, doc_name)
		VALUES ($1, $2, $3, $4, $5)`,
		entry.Timestamp, entry.Level, entry.Component, entry.Message, entry.DocName,
	)
	return err
}

func (r *pgErrorLogRepository) recent(n int) ([]*ErrorLogEntry, error) {
	rows, err := r.db.Query(`
		SELECT timestamp, level, component, message, doc_name
		FROM error_log
		ORDER BY timestamp DESC
		LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]*ErrorLogEntry, 0)
	for rows.Next() {
		e := &ErrorLogEntry{}
		if err := rows.Scan(&e.Timestamp, &e.Level, &e.Component, &e.Message, &e.DocName); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
