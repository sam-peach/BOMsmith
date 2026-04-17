package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
)

// matchFeedbackRepository records user accept/reject decisions on similarity candidates.
type matchFeedbackRepository interface {
	record(fb *MatchFeedback, orgID string) error
}

// ── pgMatchFeedbackRepository ─────────────────────────────────────────────────

type pgMatchFeedbackRepository struct {
	db *sql.DB
}

func (r *pgMatchFeedbackRepository) record(fb *MatchFeedback, orgID string) error {
	var bdJSON *string
	if fb.ScoreBreakdown != nil {
		b, err := json.Marshal(fb.ScoreBreakdown)
		if err != nil {
			return fmt.Errorf("marshal score_breakdown: %w", err)
		}
		s := string(b)
		bdJSON = &s
	}

	_, err := r.db.Exec(`
		INSERT INTO match_feedback
			(id, organization_id, drawing_id, candidate_id, action, score, score_breakdown)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		newID(), orgID,
		fb.DrawingID, fb.CandidateID, fb.Action, fb.Score, bdJSON,
	)
	if err != nil {
		log.Printf("match_feedback.record error: %v", err)
		return fmt.Errorf("record feedback: %w", err)
	}
	return nil
}
