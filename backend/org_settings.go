package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

// defaultExportConfig is returned when no config has been saved for an org.
// Column 2 is intentionally blank so the paste lands in the correct SAP fields.
var defaultExportConfig = &ExportConfig{
	Columns:       []string{"internalPartNumber", "empty", "quantity"},
	IncludeHeader: false,
}

// validExportColumns maps every recognised column key to its display name.
// The display name is used as the header row label when IncludeHeader is true.
var validExportColumns = map[string]string{
	"lineNumber":             "Line",
	"description":            "Description",
	"quantity":               "Quantity",
	"unit":                   "Unit",
	"customerPartNumber":     "Customer Part Number",
	"internalPartNumber":     "Internal Part Number",
	"manufacturerPartNumber": "Manufacturer Part Number",
	"notes":                  "Notes",
	"empty":                  "",
}

// orgSettingsRepository stores and retrieves per-org configuration.
type orgSettingsRepository interface {
	getExportConfig(orgID string) (*ExportConfig, error)
	saveExportConfig(cfg *ExportConfig, orgID string) error
}

// ── memOrgSettingsRepository ──────────────────────────────────────────────────

type memOrgSettingsRepository struct {
	mu   sync.RWMutex
	data map[string]*ExportConfig // keyed by orgID
}

func (r *memOrgSettingsRepository) getExportConfig(orgID string) (*ExportConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.data != nil {
		if cfg, ok := r.data[orgID]; ok {
			return cfg, nil
		}
	}
	return defaultExportConfig, nil
}

func (r *memOrgSettingsRepository) saveExportConfig(cfg *ExportConfig, orgID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		r.data = make(map[string]*ExportConfig)
	}
	r.data[orgID] = cfg
	return nil
}

// ── pgOrgSettingsRepository ───────────────────────────────────────────────────

type pgOrgSettingsRepository struct {
	db *sql.DB
}

func (r *pgOrgSettingsRepository) getExportConfig(orgID string) (*ExportConfig, error) {
	var columnsJSON []byte
	var includeHeader bool
	err := r.db.QueryRow(`
		SELECT export_columns, export_include_header
		FROM org_settings WHERE org_id = $1`, orgID,
	).Scan(&columnsJSON, &includeHeader)

	if err == sql.ErrNoRows {
		return defaultExportConfig, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get export config: %w", err)
	}

	var columns []string
	if err := json.Unmarshal(columnsJSON, &columns); err != nil {
		log.Printf("org_settings: corrupt columns JSON for org %s, returning default: %v", orgID, err)
		return defaultExportConfig, nil
	}
	return &ExportConfig{Columns: columns, IncludeHeader: includeHeader}, nil
}

func (r *pgOrgSettingsRepository) saveExportConfig(cfg *ExportConfig, orgID string) error {
	b, err := json.Marshal(cfg.Columns)
	if err != nil {
		return fmt.Errorf("marshal columns: %w", err)
	}
	_, err = r.db.Exec(`
		INSERT INTO org_settings (org_id, export_columns, export_include_header)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id) DO UPDATE SET
			export_columns        = EXCLUDED.export_columns,
			export_include_header = EXCLUDED.export_include_header,
			updated_at            = now()`,
		orgID, string(b), cfg.IncludeHeader,
	)
	return err
}
