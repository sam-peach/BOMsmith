package main

import "time"

type DocumentStatus string

const (
	StatusUploaded  DocumentStatus = "uploaded"
	StatusAnalyzing DocumentStatus = "analyzing"
	StatusDone      DocumentStatus = "done"
	StatusError     DocumentStatus = "error"
)

type Document struct {
	ID                 string         `json:"id"`
	OrganizationID     string         `json:"-"` // server-side only
	Filename           string         `json:"filename"`
	FilePath           string         `json:"-"` // server-side only
	Status             DocumentStatus `json:"status"`
	UploadedAt         time.Time      `json:"uploadedAt"`
	BOMRows            []BOMRow       `json:"bomRows"`
	Warnings           []string       `json:"warnings"`
	ClonedFromID       string         `json:"clonedFromId,omitempty"`
	FileSizeBytes      int64          `json:"fileSizeBytes"`
	AnalysisDurationMs int64          `json:"analysisDurationMs,omitempty"`
	ErrorMessage       string         `json:"errorMessage,omitempty"`
}

// ScoreBreakdown holds per-signal contributions to the composite similarity score.
type ScoreBreakdown struct {
	Filename float64 `json:"filename"` // Jaccard similarity of filename tokens
	CPN      float64 `json:"cpn"`      // Jaccard similarity of customer part numbers
	MPN      float64 `json:"mpn"`      // Jaccard similarity of manufacturer part numbers
}

// SimilarDocument is a lightweight summary of a past document that resembles
// the current drawing. Returned by GET /api/documents/{id}/similar.
type SimilarDocument struct {
	ID             string         `json:"id"`
	Filename       string         `json:"filename"`
	UploadedAt     time.Time      `json:"uploadedAt"`
	Score          float64        `json:"score"`          // 0.0–1.0 composite similarity
	ScoreBreakdown ScoreBreakdown `json:"scoreBreakdown"` // per-signal contributions
	MatchReasons   []string       `json:"matchReasons"`   // human-readable explanations
	BOMRowCount    int            `json:"bomRowCount"`
}

// MatchFeedback records a user's explicit accept or reject of a similarity candidate.
type MatchFeedback struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"-"` // server-side only
	DrawingID      string          `json:"drawingId"`
	CandidateID    string          `json:"candidateId"`
	Action         string          `json:"action"`                   // "accept" | "reject"
	Score          float64         `json:"score"`
	ScoreBreakdown *ScoreBreakdown `json:"scoreBreakdown,omitempty"` // nil when not captured
	CreatedAt      time.Time       `json:"createdAt"`
}

// Quantity holds a quantity value as extracted from the drawing.
// Raw is always preserved verbatim. Value/Unit are parsed from Raw.
// Normalized is reserved for future unit normalisation — currently equals Value.
type Quantity struct {
	Raw        string   `json:"raw"`
	Value      *float64 `json:"value"`
	Unit       *string  `json:"unit"`
	Normalized *float64 `json:"normalized"`
	Flags      []string `json:"flags"`
}

type BOMRow struct {
	ID                     string   `json:"id"`
	LineNumber             int      `json:"lineNumber"`
	RawLabel               string   `json:"rawLabel"`
	Description            string   `json:"description"`
	Quantity               Quantity `json:"quantity"`
	CustomerPartNumber     string   `json:"customerPartNumber"`
	InternalPartNumber     string   `json:"internalPartNumber"`
	ManufacturerPartNumber string   `json:"manufacturerPartNumber"`
	SupplierReference      string   `json:"supplierReference"`
	Supplier               string   `json:"supplier"` // "RS" | "Farnell" | "Unknown" | ""
	Notes                  string   `json:"notes"`
	Confidence             float64  `json:"confidence"` // 0.0–1.0
	Flags                  []string `json:"flags"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type User struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	Username       string    `json:"username"`
	PasswordHash   string    `json:"-"` // never serialised
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Mapping records a known cross-reference between a customer part number and
// the internal/manufacturer identifiers used in-house.
type Mapping struct {
	ID                     string    `json:"id"`
	OrganizationID         string    `json:"-"` // server-side only
	CustomerPartNumber     string    `json:"customerPartNumber"`
	InternalPartNumber     string    `json:"internalPartNumber"`
	ManufacturerPartNumber string    `json:"manufacturerPartNumber"`
	Description            string    `json:"description"`
	Source                 string    `json:"source"`     // "manual" | "inferred"
	Confidence             float64   `json:"confidence"` // 0.0–1.0
	LastUsedAt             time.Time `json:"lastUsedAt"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

type AnalysisResult struct {
	BOMRows  []BOMRow
	Warnings []string
}

// ExportConfig controls which columns appear in the SAP export and their order.
type ExportConfig struct {
	Columns       []string `json:"columns"`
	IncludeHeader bool     `json:"includeHeader"`
}

// ErrorLogEntry records a structured error or warning from the analysis pipeline.
type ErrorLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`     // "error" | "warn"
	Component string    `json:"component"` // e.g. "analysis"
	Message   string    `json:"message"`
	DocName   string    `json:"docName,omitempty"`
}

// InviteToken is a single-use link scoped to an organisation.
type InviteToken struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"-"`
	OrgName        string     `json:"orgName"`
	Token          string     `json:"token"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	UsedAt         *time.Time `json:"usedAt,omitempty"`
	UsedByUserID   string     `json:"-"`
	CreatedAt      time.Time  `json:"createdAt"`
}
