package main

import (
	cryptorand "crypto/rand"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type server struct {
	store     *documentStore
	mappings  mappingRepository
	sessions  *sessionStore
	uploadDir string
	apiKey    string
	userRepo  userRepository
}

// POST /api/documents/upload
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	const maxUpload = 32 << 20
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `form field "file" is required`)
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		writeError(w, http.StatusBadRequest, "only PDF files are accepted")
		return
	}

	magic := make([]byte, 4)
	if _, err := io.ReadFull(file, magic); err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return
	}
	if string(magic) != "%PDF" {
		writeError(w, http.StatusBadRequest, "file is not a valid PDF")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	id := newID()
	destPath := filepath.Join(s.uploadDir, id+".pdf")

	dest, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}
	defer dest.Close()

	if _, err := io.Copy(dest, file); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	doc := &Document{
		ID:         id,
		Filename:   header.Filename,
		FilePath:   destPath,
		Status:     StatusUploaded,
		UploadedAt: time.Now().UTC(),
		BOMRows:    []BOMRow{},
		Warnings:   []string{},
	}
	s.store.save(doc)
	writeJSON(w, http.StatusCreated, doc)
}

// POST /api/documents/{id}/analyze
func (s *server) analyze(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := s.store.get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	log.Printf("analyze: starting %s (%s)", doc.ID, doc.Filename)
	doc.Status = StatusAnalyzing
	s.store.save(doc)

	sd := sessionFromContext(r)
	result, err := analyzeDocument(doc, s.apiKey, &orgScopedMappings{repo: s.mappings, orgID: sd.OrgID})
	if err != nil {
		log.Printf("analyze: error for %s: %v", doc.ID, err)
		doc.Status = StatusError
		s.store.save(doc)
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	log.Printf("analyze: done %s — %d rows, %d warnings", doc.ID, len(result.BOMRows), len(result.Warnings))
	doc.BOMRows = result.BOMRows
	doc.Warnings = result.Warnings
	doc.Status = StatusDone
	s.store.save(doc)
	writeJSON(w, http.StatusOK, doc)
}

// GET /api/documents/{id}
func (s *server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := s.store.get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// GET /api/documents/{id}/bom.csv
// SAP-compatible column order: Line, Description, Quantity (RAW), Unit,
// Customer P/N, Internal P/N, Manufacturer P/N, Notes.
func (s *server) exportCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := s.store.get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	name := strings.TrimSuffix(doc.Filename, ".pdf")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-bom.csv"`, name))

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"Line", "Description", "Quantity", "Unit",
		"Customer Part Number", "Internal Part Number", "Manufacturer Part Number", "Notes",
	})
	for _, row := range doc.BOMRows {
		qty := row.Quantity.Raw
		unit := ""
		if row.Quantity.Unit != nil {
			unit = *row.Quantity.Unit
		}
		_ = cw.Write([]string{
			fmt.Sprintf("%d", row.LineNumber),
			row.Description,
			qty,
			unit,
			row.CustomerPartNumber,
			row.InternalPartNumber,
			row.ManufacturerPartNumber,
			row.Notes,
		})
	}
	cw.Flush()
}

// PUT /api/documents/{id}/bom — persists client-side edits so CSV export stays current.
func (s *server) saveBOM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := s.store.get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	var rows []BOMRow
	if err := json.NewDecoder(r.Body).Decode(&rows); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	doc.BOMRows = rows
	s.store.save(doc)
	writeJSON(w, http.StatusOK, doc)
}

// POST /api/mappings — create or update a single mapping.
func (s *server) saveMapping(w http.ResponseWriter, r *http.Request) {
	var m Mapping
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(m.CustomerPartNumber) == "" {
		writeError(w, http.StatusBadRequest, "customerPartNumber is required")
		return
	}
	if m.Source == "" {
		m.Source = "manual"
	}
	if m.Confidence == 0 {
		m.Confidence = 1.0
	}
	sd := sessionFromContext(r)
	if err := s.mappings.save(&m, sd.OrgID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("mapping saved: %s → internal=%s mfr=%s", m.CustomerPartNumber, m.InternalPartNumber, m.ManufacturerPartNumber)
	writeJSON(w, http.StatusOK, &m)
}

// GET /api/mappings — list all stored mappings.
func (s *server) listMappings(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)
	all := s.mappings.all(sd.OrgID)
	if all == nil {
		all = []*Mapping{}
	}
	writeJSON(w, http.StatusOK, all)
}

// POST /api/mappings/upload — bulk import mappings from a CSV file.
// Expected columns (header row required):
//
//	CustomerPartNumber, InternalPartNumber, ManufacturerPartNumber, Description
func (s *server) uploadMappings(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)
	const maxUpload = 4 << 20
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `form field "file" is required`)
		return
	}
	defer file.Close()

	cr := csv.NewReader(file)
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read CSV header row")
		return
	}

	// Build column index map (case-insensitive).
	colIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	required := []string{"customerpartnumber"}
	for _, col := range required {
		if _, ok := colIdx[col]; !ok {
			writeError(w, http.StatusBadRequest, "CSV must include a CustomerPartNumber column")
			return
		}
	}

	get := func(row []string, col string) string {
		i, ok := colIdx[col]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	var saved, skipped int
	for {
		row, err := cr.Read()
		if err != nil {
			break // EOF or error — stop processing
		}
		cpn := get(row, "customerpartnumber")
		if cpn == "" {
			skipped++
			continue
		}
		m := &Mapping{
			CustomerPartNumber:     cpn,
			InternalPartNumber:     get(row, "internalpartnumber"),
			ManufacturerPartNumber: get(row, "manufacturerpartnumber"),
			Description:            get(row, "description"),
			Source:                 "csv-upload",
			Confidence:             1.0,
		}
		if err := s.mappings.save(m, sd.OrgID); err != nil {
			log.Printf("mapping upload: skipping %q: %v", cpn, err)
			skipped++
			continue
		}
		saved++
	}

	log.Printf("mapping upload: saved=%d skipped=%d", saved, skipped)
	writeJSON(w, http.StatusOK, map[string]int{"saved": saved, "skipped": skipped})
}

// PUT /api/users/me/password — changes the authenticated user's password.
func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)

	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.CurrentPassword) == "" || strings.TrimSpace(req.NewPassword) == "" {
		writeError(w, http.StatusBadRequest, "currentPassword and newPassword are required")
		return
	}

	user, err := s.userRepo.findByID(sd.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := s.userRepo.updatePassword(sd.UserID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/users/me — returns the current user's IDs.
func (s *server) getMe(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)
	writeJSON(w, http.StatusOK, map[string]string{
		"userId": sd.UserID,
		"orgId":  sd.OrgID,
	})
}

// POST /api/users — create a new user within the caller's organisation.
func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	sd := sessionFromContext(r)

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

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	user, err := s.userRepo.createUser(sd.OrgID, req.Username, string(hash))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func newID() string {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
