package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
)

// documentRepository is the storage abstraction for Documents.
type documentRepository interface {
	save(doc *Document)
	get(id string) (*Document, error)
	// list returns all documents for the given org across all statuses.
	list(orgID string) ([]*Document, error)
	// listByOrg returns all status=done documents for the given org,
	// used by the similarity engine to find repeat parts.
	listByOrg(orgID string) ([]*Document, error)
	// delete removes a document by ID. Returns nil if the document does not exist.
	delete(id string) error
	// resetAnalyzing transitions any analyzing document to error state.
	// Called at startup to clean up jobs killed by a server restart.
	resetAnalyzing() error
}

// ── pgDocumentStore ───────────────────────────────────────────────────────────

type pgDocumentStore struct {
	db *sql.DB
}

func (s *pgDocumentStore) save(doc *Document) {
	bomJSON, err := json.Marshal(doc.BOMRows)
	if err != nil {
		log.Printf("pgDocumentStore.save: marshal bom_rows: %v", err)
		return
	}
	warnJSON, err := json.Marshal(doc.Warnings)
	if err != nil {
		log.Printf("pgDocumentStore.save: marshal warnings: %v", err)
		return
	}

	var clonedFrom *string
	if doc.ClonedFromID != "" {
		clonedFrom = &doc.ClonedFromID
	}

	var analysisDuration *int64
	if doc.AnalysisDurationMs > 0 {
		v := doc.AnalysisDurationMs
		analysisDuration = &v
	}

	var errorMessage *string
	if doc.ErrorMessage != "" {
		errorMessage = &doc.ErrorMessage
	}

	_, err = s.db.Exec(`
		INSERT INTO documents
			(id, organization_id, filename, status, bom_rows, warnings, cloned_from_id, uploaded_at,
			 file_size_bytes, analysis_duration_ms, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			status               = EXCLUDED.status,
			bom_rows             = EXCLUDED.bom_rows,
			warnings             = EXCLUDED.warnings,
			cloned_from_id       = EXCLUDED.cloned_from_id,
			file_size_bytes      = EXCLUDED.file_size_bytes,
			analysis_duration_ms = EXCLUDED.analysis_duration_ms,
			error_message        = EXCLUDED.error_message,
			updated_at           = now()`,
		doc.ID, doc.OrganizationID, doc.Filename, string(doc.Status),
		string(bomJSON), string(warnJSON), clonedFrom, doc.UploadedAt,
		doc.FileSizeBytes, analysisDuration, errorMessage,
	)
	if err != nil {
		log.Printf("pgDocumentStore.save error for %s: %v", doc.ID, err)
	}
}

func (s *pgDocumentStore) get(id string) (*Document, error) {
	var doc Document
	var bomJSON, warnJSON string
	var clonedFrom sql.NullString
	var analysisDuration sql.NullInt64
	var errorMessage sql.NullString

	err := s.db.QueryRow(`
		SELECT id, organization_id, filename, status, bom_rows, warnings, cloned_from_id, uploaded_at,
		       file_size_bytes, analysis_duration_ms, error_message
		FROM documents WHERE id = $1`, id,
	).Scan(&doc.ID, &doc.OrganizationID, &doc.Filename, &doc.Status,
		&bomJSON, &warnJSON, &clonedFrom, &doc.UploadedAt,
		&doc.FileSizeBytes, &analysisDuration, &errorMessage)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("document %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("pgDocumentStore.get: %w", err)
	}

	if err := json.Unmarshal([]byte(bomJSON), &doc.BOMRows); err != nil {
		return nil, fmt.Errorf("pgDocumentStore.get: unmarshal bom_rows: %w", err)
	}
	if err := json.Unmarshal([]byte(warnJSON), &doc.Warnings); err != nil {
		return nil, fmt.Errorf("pgDocumentStore.get: unmarshal warnings: %w", err)
	}
	if clonedFrom.Valid {
		doc.ClonedFromID = clonedFrom.String
	}
	if analysisDuration.Valid {
		doc.AnalysisDurationMs = analysisDuration.Int64
	}
	if errorMessage.Valid {
		doc.ErrorMessage = errorMessage.String
	}
	return &doc, nil
}

func (s *pgDocumentStore) listByOrg(orgID string) ([]*Document, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, filename, status, bom_rows, warnings, cloned_from_id, uploaded_at,
		       file_size_bytes, analysis_duration_ms, error_message
		FROM documents
		WHERE organization_id = $1 AND status = 'done'
		ORDER BY uploaded_at DESC
		LIMIT 100`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("pgDocumentStore.listByOrg: %w", err)
	}
	defer rows.Close()

	var out []*Document
	for rows.Next() {
		var doc Document
		var bomJSON, warnJSON string
		var clonedFrom sql.NullString
		var analysisDuration sql.NullInt64
		var errorMessage sql.NullString

		if err := rows.Scan(&doc.ID, &doc.OrganizationID, &doc.Filename, &doc.Status,
			&bomJSON, &warnJSON, &clonedFrom, &doc.UploadedAt,
			&doc.FileSizeBytes, &analysisDuration, &errorMessage); err != nil {
			log.Printf("pgDocumentStore.listByOrg scan: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(bomJSON), &doc.BOMRows); err != nil {
			log.Printf("pgDocumentStore.listByOrg unmarshal bom_rows: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(warnJSON), &doc.Warnings); err != nil {
			log.Printf("pgDocumentStore.listByOrg unmarshal warnings: %v", err)
			continue
		}
		if clonedFrom.Valid {
			doc.ClonedFromID = clonedFrom.String
		}
		if analysisDuration.Valid {
			doc.AnalysisDurationMs = analysisDuration.Int64
		}
		if errorMessage.Valid {
			doc.ErrorMessage = errorMessage.String
		}
		out = append(out, &doc)
	}
	return out, nil
}

func (s *pgDocumentStore) list(orgID string) ([]*Document, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, filename, status, bom_rows, warnings, cloned_from_id, uploaded_at,
		       file_size_bytes, analysis_duration_ms, error_message
		FROM documents
		WHERE organization_id = $1
		ORDER BY uploaded_at DESC
		LIMIT 200`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("pgDocumentStore.list: %w", err)
	}
	defer rows.Close()

	var out []*Document
	for rows.Next() {
		var doc Document
		var bomJSON, warnJSON string
		var clonedFrom sql.NullString
		var analysisDuration sql.NullInt64
		var errorMessage sql.NullString

		if err := rows.Scan(&doc.ID, &doc.OrganizationID, &doc.Filename, &doc.Status,
			&bomJSON, &warnJSON, &clonedFrom, &doc.UploadedAt,
			&doc.FileSizeBytes, &analysisDuration, &errorMessage); err != nil {
			log.Printf("pgDocumentStore.list scan: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(bomJSON), &doc.BOMRows); err != nil {
			log.Printf("pgDocumentStore.list unmarshal bom_rows: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(warnJSON), &doc.Warnings); err != nil {
			log.Printf("pgDocumentStore.list unmarshal warnings: %v", err)
			continue
		}
		if clonedFrom.Valid {
			doc.ClonedFromID = clonedFrom.String
		}
		if analysisDuration.Valid {
			doc.AnalysisDurationMs = analysisDuration.Int64
		}
		if errorMessage.Valid {
			doc.ErrorMessage = errorMessage.String
		}
		out = append(out, &doc)
	}
	return out, nil
}

func (s *pgDocumentStore) delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM documents WHERE id = $1`, id)
	return err
}

func (s *pgDocumentStore) resetAnalyzing() error {
	_, err := s.db.Exec(`
		UPDATE documents
		SET status        = 'error',
		    error_message = 'Analysis was interrupted by a server restart — please re-analyse.',
		    updated_at    = now()
		WHERE status = 'analyzing'`)
	return err
}
