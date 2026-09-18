package db

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/baiirun/prog/internal/model"
)

// CreateLearning inserts a new learning and its concept associations.
// Creates concepts that don't exist yet.
func (db *DB) CreateLearning(l *model.Learning) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize files to JSON
	filesJSON := "[]"
	if len(l.Files) > 0 {
		b, err := json.Marshal(l.Files)
		if err != nil {
			return fmt.Errorf("failed to marshal files: %w", err)
		}
		filesJSON = string(b)
	}

	// Insert learning
	_, err = tx.Exec(`
		INSERT INTO learnings (id, created_at, updated_at, task_id, summary, detail, files, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, l.ID, l.CreatedAt, l.UpdatedAt, l.TaskID, l.Summary, l.Detail, filesJSON, l.Status)
	if err != nil {
		return fmt.Errorf("failed to insert learning: %w", err)
	}

	// Ensure concepts exist and create associations
	for _, conceptName := range l.Concepts {
		// Check if concept exists
		var conceptID string
		err = tx.QueryRow(`SELECT id FROM concepts WHERE name = ?`, conceptName).Scan(&conceptID)
		if err != nil {
			// Concept doesn't exist, create it
			conceptID = model.GenerateConceptID()
			_, err = tx.Exec(`
				INSERT INTO concepts (id, name, last_updated)
				VALUES (?, ?, ?)
			`, conceptID, conceptName, l.UpdatedAt)
			if err != nil {
				return fmt.Errorf("failed to create concept %q: %w", conceptName, err)
			}
		} else {
			// Update last_updated
			_, err = tx.Exec(`UPDATE concepts SET last_updated = ? WHERE id = ?`, l.UpdatedAt, conceptID)
			if err != nil {
				return fmt.Errorf("failed to update concept %q: %w", conceptName, err)
			}
		}

		// Create association
		_, err = tx.Exec(`
			INSERT INTO learning_concepts (learning_id, concept_id)
			VALUES (?, ?)
		`, l.ID, conceptID)
		if err != nil {
			return fmt.Errorf("failed to create concept association: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetLearning retrieves a learning by ID.
func (db *DB) GetLearning(id string) (*model.Learning, error) {
	var l model.Learning
	var filesJSON string
	var taskID *string

	err := db.QueryRow(`
		SELECT id, created_at, updated_at, task_id, summary, detail, files, status
		FROM learnings WHERE id = ?
	`, id).Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt, &taskID, &l.Summary, &l.Detail, &filesJSON, &l.Status)
	if err != nil {
		return nil, fmt.Errorf("learning not found: %s", id)
	}
	l.TaskID = taskID

	// Parse files JSON
	if filesJSON != "" && filesJSON != "[]" {
		if err := json.Unmarshal([]byte(filesJSON), &l.Files); err != nil {
			return nil, fmt.Errorf("failed to unmarshal files: %w", err)
		}
	}

	// Get associated concepts
	rows, err := db.Query(`
		SELECT c.name FROM learning_concepts lc
		JOIN concepts c ON c.id = lc.concept_id
		WHERE lc.learning_id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get concepts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var concept string
		if err := rows.Scan(&concept); err != nil {
			return nil, fmt.Errorf("failed to scan concept: %w", err)
		}
		l.Concepts = append(l.Concepts, concept)
	}

	return &l, nil
}

// ListConcepts returns every concept, sorted by learning count (most used first).
func (db *DB) ListConcepts(sortByRecent bool) ([]model.Concept, error) {
	orderBy := "count DESC, c.name"
	if sortByRecent {
		orderBy = "c.last_updated DESC, c.name"
	}

	rows, err := db.Query(`
		SELECT c.id, c.name, c.summary, c.last_updated,
			(SELECT COUNT(*) FROM learning_concepts lc WHERE lc.concept_id = c.id) as count
		FROM concepts c
		ORDER BY ` + orderBy)
	if err != nil {
		return nil, fmt.Errorf("failed to list concepts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var concepts []model.Concept
	for rows.Next() {
		var c model.Concept
		var summary *string
		if err := rows.Scan(&c.ID, &c.Name, &summary, &c.LastUpdated, &c.LearningCount); err != nil {
			return nil, fmt.Errorf("failed to scan concept: %w", err)
		}
		if summary != nil {
			c.Summary = *summary
		}
		concepts = append(concepts, c)
	}

	return concepts, nil
}

// EnsureConcept creates a concept if it doesn't exist.
func (db *DB) EnsureConcept(name string) error {
	_, err := db.Exec(`
		INSERT INTO concepts (id, name, last_updated)
		VALUES (?, ?, ?)
		ON CONFLICT (name) DO NOTHING
	`, model.GenerateConceptID(), name, time.Now())
	return err
}

// SetConceptSummary updates a concept's summary.
func (db *DB) SetConceptSummary(name, summary string) error {
	result, err := db.Exec(`
		UPDATE concepts SET summary = ?, last_updated = ?
		WHERE name = ?
	`, summary, time.Now(), name)
	if err != nil {
		return fmt.Errorf("failed to update concept: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("concept not found: %s", name)
	}
	return nil
}

// UpdateLearningSummary updates a learning's summary.
func (db *DB) UpdateLearningSummary(id, summary string) error {
	result, err := db.Exec(`
		UPDATE learnings SET summary = ?, updated_at = ?
		WHERE id = ?
	`, summary, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update learning: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("learning not found: %s", id)
	}
	return nil
}

// UpdateLearningStatus updates a learning's status (active, stale, archived).
func (db *DB) UpdateLearningStatus(id string, status model.LearningStatus) error {
	result, err := db.Exec(`
		UPDATE learnings SET status = ?, updated_at = ?
		WHERE id = ?
	`, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update learning status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("learning not found: %s", id)
	}
	return nil
}

// UpdateLearningDetail updates a learning's detail.
func (db *DB) UpdateLearningDetail(id, detail string) error {
	result, err := db.Exec(`
		UPDATE learnings SET detail = ?, updated_at = ?
		WHERE id = ?
	`, detail, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update learning detail: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("learning not found: %s", id)
	}
	return nil
}

// DeleteLearning removes a learning and its concept associations.
func (db *DB) DeleteLearning(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete concept associations
	_, err = tx.Exec(`DELETE FROM learning_concepts WHERE learning_id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete concept associations: %w", err)
	}

	// Delete learning
	result, err := tx.Exec(`DELETE FROM learnings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete learning: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("learning not found: %s", id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// RenameConcept changes a concept's name.
func (db *DB) RenameConcept(oldName, newName string) error {
	result, err := db.Exec(`
		UPDATE concepts SET name = ?, last_updated = ?
		WHERE name = ?
	`, newName, time.Now(), oldName)
	if err != nil {
		return fmt.Errorf("failed to rename concept: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("concept not found: %s", oldName)
	}
	return nil
}

// GetLearningsByConcepts returns learnings that have any of the specified concepts.
// Only returns active learnings by default. Results are sorted by created_at desc.
func (db *DB) GetLearningsByConcepts(conceptNames []string, includeStale bool) ([]model.Learning, error) {
	if len(conceptNames) == 0 {
		return nil, nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(conceptNames))
	args := make([]interface{}, 0, len(conceptNames))
	for i, name := range conceptNames {
		placeholders[i] = "?"
		args = append(args, name)
	}

	statusFilter := "AND l.status = 'active'"
	if includeStale {
		statusFilter = "AND l.status IN ('active', 'stale')"
	}

	query := `
		SELECT DISTINCT l.id, l.created_at, l.updated_at, l.task_id,
			l.summary, l.detail, l.files, l.status
		FROM learnings l
		JOIN learning_concepts lc ON lc.learning_id = l.id
		JOIN concepts c ON c.id = lc.concept_id
		WHERE c.name IN (` + strings.Join(placeholders, ",") + `)
		` + statusFilter + `
		ORDER BY l.created_at DESC
	`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query learnings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var learnings []model.Learning
	for rows.Next() {
		var l model.Learning
		var filesJSON string
		var taskID *string
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt, &taskID,
			&l.Summary, &l.Detail, &filesJSON, &l.Status); err != nil {
			return nil, fmt.Errorf("failed to scan learning: %w", err)
		}
		l.TaskID = taskID

		// Parse files JSON
		if filesJSON != "" && filesJSON != "[]" {
			if err := json.Unmarshal([]byte(filesJSON), &l.Files); err != nil {
				return nil, fmt.Errorf("failed to unmarshal files: %w", err)
			}
		}

		// Get associated concepts
		conceptRows, err := db.Query(`
			SELECT c.name FROM learning_concepts lc
			JOIN concepts c ON c.id = lc.concept_id
			WHERE lc.learning_id = ?
		`, l.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get concepts: %w", err)
		}
		for conceptRows.Next() {
			var concept string
			if err := conceptRows.Scan(&concept); err != nil {
				_ = conceptRows.Close()
				return nil, fmt.Errorf("failed to scan concept: %w", err)
			}
			l.Concepts = append(l.Concepts, concept)
		}
		_ = conceptRows.Close()

		learnings = append(learnings, l)
	}

	return learnings, nil
}

// SearchLearnings performs full-text search on learnings.
// Returns learnings matching the query, sorted by relevance.
func (db *DB) SearchLearnings(query string, includeStale bool) ([]model.Learning, error) {
	statusFilter := "AND l.status = 'active'"
	if includeStale {
		statusFilter = "AND l.status IN ('active', 'stale')"
	}

	sqlQuery := `
		SELECT l.id, l.created_at, l.updated_at, l.task_id,
			l.summary, l.detail, l.files, l.status
		FROM learnings l
		JOIN learnings_fts fts ON l.rowid = fts.rowid
		WHERE learnings_fts MATCH ?
		` + statusFilter + `
		ORDER BY rank
	`

	rows, err := db.Query(sqlQuery, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search learnings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var learnings []model.Learning
	for rows.Next() {
		var l model.Learning
		var filesJSON string
		var taskID *string
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt, &taskID,
			&l.Summary, &l.Detail, &filesJSON, &l.Status); err != nil {
			return nil, fmt.Errorf("failed to scan learning: %w", err)
		}
		l.TaskID = taskID

		// Parse files JSON
		if filesJSON != "" && filesJSON != "[]" {
			if err := json.Unmarshal([]byte(filesJSON), &l.Files); err != nil {
				return nil, fmt.Errorf("failed to unmarshal files: %w", err)
			}
		}

		// Get associated concepts
		conceptRows, err := db.Query(`
			SELECT c.name FROM learning_concepts lc
			JOIN concepts c ON c.id = lc.concept_id
			WHERE lc.learning_id = ?
		`, l.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get concepts: %w", err)
		}
		for conceptRows.Next() {
			var concept string
			if err := conceptRows.Scan(&concept); err != nil {
				_ = conceptRows.Close()
				return nil, fmt.Errorf("failed to scan concept: %w", err)
			}
			l.Concepts = append(l.Concepts, concept)
		}
		_ = conceptRows.Close()

		learnings = append(learnings, l)
	}

	return learnings, nil
}

// searchKnowledgeWeights are internal ranking weights only. Callers see MatchReason
// signals, never an aggregate score.
var searchKnowledgeWeights = map[string]int{
	model.MatchExactSummary:    8,
	model.MatchFTSSummary:      4,
	model.MatchConceptName:     4,
	model.MatchConceptSummary:  3,
	model.MatchTaskTitle:       3,
	model.MatchTaskDescription: 2,
	model.MatchFTSDetail:       2,
	model.MatchFilePath:        2,
}

// SearchKnowledge ranks active learnings using text, concepts, task title and
// description, and existing file references. Every hit reports why it matched.
// Stale learnings are excluded unless q.IncludeStale is set. Ordering is
// deterministic: higher internal signal weight first, then learning ID ascending.
func (db *DB) SearchKnowledge(q model.KnowledgeQuery) ([]model.KnowledgeHit, error) {
	text := strings.TrimSpace(q.Text)
	var taskTitle, taskDesc string
	if q.TaskID != "" {
		item, err := db.GetItem(q.TaskID)
		if err != nil {
			return nil, err
		}
		taskTitle = strings.TrimSpace(item.Title)
		taskDesc = strings.TrimSpace(item.Description)
	}
	requestedConcepts := knowledgeNormalizeConceptList(q.Concepts)
	if text == "" && taskTitle == "" && taskDesc == "" && len(requestedConcepts) == 0 {
		return nil, nil
	}

	learnings, err := db.GetAllLearnings(q.IncludeStale)
	if err != nil {
		return nil, err
	}
	concepts, err := db.ListConcepts(false)
	if err != nil {
		return nil, err
	}
	conceptByName := make(map[string]model.Concept, len(concepts))
	for _, c := range concepts {
		conceptByName[c.Name] = c
	}

	ftsTextIDs, err := db.knowledgeFTSIDs(text, q.IncludeStale)
	if err != nil {
		return nil, err
	}

	textTokens := knowledgeTokenize(text)
	titleTokens := knowledgeTokenize(taskTitle)
	descTokens := knowledgeTokenize(taskDesc)
	contextTokens := append(append(append([]string{}, textTokens...), titleTokens...), descTokens...)
	titleMeaningful := knowledgeMeaningfulTokens(titleTokens)
	descMeaningful := knowledgeMeaningfulTokens(descTokens)
	textLower := strings.ToLower(text)

	type scored struct {
		hit   model.KnowledgeHit
		score int
	}
	var ranked []scored

	for _, l := range learnings {
		var reasons []model.MatchReason
		seen := map[string]bool{}
		add := func(signal, detail string) {
			key := signal + "\x00" + detail
			if seen[key] {
				return
			}
			seen[key] = true
			reasons = append(reasons, model.MatchReason{Signal: signal, Detail: detail})
		}

		summaryTokens := knowledgeTokenize(l.Summary)
		detailTokens := knowledgeTokenize(l.Detail)
		learningTextTokens := append(append([]string{}, summaryTokens...), detailTokens...)
		learningConceptTokens := make([]string, 0, len(l.Concepts))
		for _, name := range l.Concepts {
			learningConceptTokens = append(learningConceptTokens, knowledgeTokenize(name)...)
		}
		learningMatchTokens := append(append([]string{}, learningTextTokens...), learningConceptTokens...)

		if text != "" && strings.Contains(strings.ToLower(l.Summary), textLower) {
			add(model.MatchExactSummary, text)
		}

		if ftsTextIDs[l.ID] {
			if len(knowledgeWholeTokenOverlap(summaryTokens, textTokens)) > 0 {
				add(model.MatchFTSSummary, text)
			}
			if len(knowledgeWholeTokenOverlap(detailTokens, textTokens)) > 0 {
				add(model.MatchFTSDetail, text)
			}
		}

		for _, name := range l.Concepts {
			if knowledgeRequestedConcept(requestedConcepts, name) {
				add(model.MatchConceptName, name)
			} else if knowledgeConceptNameInTokens(name, contextTokens) {
				add(model.MatchConceptName, name)
			}
			c := conceptByName[name]
			if c.Summary != "" && text != "" {
				sumTokens := knowledgeTokenize(c.Summary)
				if strings.Contains(strings.ToLower(c.Summary), textLower) ||
					len(knowledgeWholeTokenOverlap(sumTokens, textTokens)) > 0 {
					add(model.MatchConceptSummary, name)
				}
			}
		}

		if len(titleMeaningful) > 0 {
			if matched := knowledgeWholeTokenOverlap(learningMatchTokens, titleMeaningful); len(matched) > 0 {
				add(model.MatchTaskTitle, strings.Join(matched, " "))
			}
		}
		if len(descMeaningful) > 0 {
			if matched := knowledgeWholeTokenOverlap(learningMatchTokens, descMeaningful); len(matched) > 0 {
				add(model.MatchTaskDescription, strings.Join(matched, " "))
			}
		}

		contextLower := strings.ToLower(strings.Join([]string{text, taskTitle, taskDesc}, " "))
		for _, file := range l.Files {
			fileLower := strings.ToLower(file)
			if fileLower == "" {
				continue
			}
			if (text != "" && (strings.Contains(textLower, fileLower) || strings.Contains(fileLower, textLower))) ||
				(taskTitle != "" && strings.Contains(strings.ToLower(taskTitle), fileLower)) ||
				(taskDesc != "" && strings.Contains(strings.ToLower(taskDesc), fileLower)) ||
				(contextLower != "" && strings.Contains(contextLower, fileLower)) {
				add(model.MatchFilePath, file)
			}
		}

		if len(reasons) == 0 {
			continue
		}

		score := 0
		for _, r := range reasons {
			score += searchKnowledgeWeights[r.Signal]
		}
		ranked = append(ranked, scored{
			hit:   model.KnowledgeHit{Learning: l, Reasons: reasons},
			score: score,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].hit.Learning.ID < ranked[j].hit.Learning.ID
	})

	hits := make([]model.KnowledgeHit, len(ranked))
	for i := range ranked {
		hits[i] = ranked[i].hit
	}
	return hits, nil
}

func (db *DB) knowledgeFTSIDs(query string, includeStale bool) (map[string]bool, error) {
	ids := map[string]bool{}
	ftsQuery := knowledgeFTSQuery(query)
	if ftsQuery == "" {
		return ids, nil
	}
	found, err := db.SearchLearnings(ftsQuery, includeStale)
	if err != nil {
		return nil, err
	}
	for _, l := range found {
		ids[l.ID] = true
	}
	return ids, nil
}

// knowledgeFTSQuery builds a safe FTS5 MATCH expression from alphanumeric tokens.
func knowledgeFTSQuery(s string) string {
	tokens := knowledgeTokenize(s)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		parts[i] = `"` + tok + `"`
	}
	return strings.Join(parts, " ")
}

// knowledgeBoilerplate are generic task/prose terms excluded from task-context overlap.
var knowledgeBoilerplate = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"to": true, "in": true, "on": true, "for": true, "with": true, "from": true,
	"by": true, "at": true, "as": true, "is": true, "be": true, "are": true,
	"was": true, "were": true, "this": true, "that": true, "it": true, "its": true,
	"into": true, "via": true, "about": true, "around": true, "over": true,
	"fix": true, "fixes": true, "fixed": true, "fixing": true,
	"bug": true, "bugs": true, "issue": true, "issues": true, "task": true,
	"add": true, "update": true, "change": true, "make": true, "implement": true,
	"investigate": true, "improve": true, "rework": true, "review": true,
	"must": true, "should": true, "need": true, "needs": true, "see": true,
	"use": true, "using": true, "when": true, "how": true, "what": true,
	"why": true, "do": true, "does": true, "not": true, "no": true, "yes": true,
	"all": true, "any": true, "some": true, "new": true, "old": true,
}

func knowledgeTokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	var out []string
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		out = append(out, f)
	}
	return out
}

func knowledgeMeaningfulTokens(tokens []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range tokens {
		if knowledgeBoilerplate[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func knowledgeTokenSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		set[tok] = true
	}
	return set
}

// knowledgeWholeTokenOverlap returns needle tokens that appear as whole tokens in
// haystack, preserving needle order and dropping duplicates.
func knowledgeWholeTokenOverlap(haystack, needles []string) []string {
	set := knowledgeTokenSet(haystack)
	var out []string
	seen := map[string]bool{}
	for _, n := range needles {
		if !set[n] || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func knowledgeConceptNameInTokens(conceptName string, contextTokens []string) bool {
	conceptToks := knowledgeTokenize(conceptName)
	if len(conceptToks) == 0 {
		return false
	}
	if len(conceptToks) == 1 {
		return knowledgeTokenSet(contextTokens)[conceptToks[0]]
	}
	for i := 0; i+len(conceptToks) <= len(contextTokens); i++ {
		match := true
		for j := range conceptToks {
			if contextTokens[i+j] != conceptToks[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func knowledgeNormalizeConceptList(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

func knowledgeRequestedConcept(requested []string, learningConcept string) bool {
	for _, req := range requested {
		if strings.EqualFold(req, learningConcept) {
			return true
		}
	}
	return false
}

// ConceptStats holds statistics for a concept.
type ConceptStats struct {
	Name          string
	LearningCount int
	OldestAge     *time.Duration // nil if no learnings
}

// ListConceptsWithStats returns all concepts with learning count and oldest learning age.
func (db *DB) ListConceptsWithStats() ([]ConceptStats, error) {
	rows, err := db.Query(`
		SELECT c.name,
			COUNT(l.id) as count,
			MIN(l.created_at) as oldest
		FROM concepts c
		LEFT JOIN learning_concepts lc ON lc.concept_id = c.id
		LEFT JOIN learnings l ON l.id = lc.learning_id AND l.status = 'active'
		GROUP BY c.id
		ORDER BY count DESC, c.name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list concept stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats []ConceptStats
	now := time.Now()
	for rows.Next() {
		var s ConceptStats
		var oldestStr *string
		if err := rows.Scan(&s.Name, &s.LearningCount, &oldestStr); err != nil {
			return nil, fmt.Errorf("failed to scan concept stats: %w", err)
		}
		if oldestStr != nil && *oldestStr != "" {
			// Parse the timestamp string - Go's default format with monotonic clock suffix
			// Format: "2006-01-02 15:04:05.999999999 -0700 MST m=+0.000000000"
			str := *oldestStr
			// Strip monotonic clock suffix if present
			if idx := strings.Index(str, " m="); idx > 0 {
				str = str[:idx]
			}
			oldest, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", str)
			if err != nil {
				oldest, err = time.Parse(time.RFC3339Nano, str)
			}
			if err == nil {
				age := now.Sub(oldest)
				s.OldestAge = &age
			}
		}
		stats = append(stats, s)
	}

	return stats, nil
}

// GetAllLearnings returns every learning, sorted by created_at desc.
// Only returns active learnings by default.
func (db *DB) GetAllLearnings(includeStale bool) ([]model.Learning, error) {
	statusFilter := "AND l.status = 'active'"
	if includeStale {
		statusFilter = "AND l.status IN ('active', 'stale')"
	}

	query := `
		SELECT l.id, l.created_at, l.updated_at, l.task_id,
			l.summary, l.detail, l.files, l.status
		FROM learnings l
		WHERE 1 = 1
		` + statusFilter + `
		ORDER BY l.created_at DESC
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query learnings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var learnings []model.Learning
	for rows.Next() {
		var l model.Learning
		var filesJSON string
		var taskID *string
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.UpdatedAt, &taskID,
			&l.Summary, &l.Detail, &filesJSON, &l.Status); err != nil {
			return nil, fmt.Errorf("failed to scan learning: %w", err)
		}
		l.TaskID = taskID

		// Parse files JSON
		if filesJSON != "" && filesJSON != "[]" {
			if err := json.Unmarshal([]byte(filesJSON), &l.Files); err != nil {
				return nil, fmt.Errorf("failed to unmarshal files: %w", err)
			}
		}

		// Get associated concepts
		conceptRows, err := db.Query(`
			SELECT c.name FROM learning_concepts lc
			JOIN concepts c ON c.id = lc.concept_id
			WHERE lc.learning_id = ?
		`, l.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get concepts: %w", err)
		}
		for conceptRows.Next() {
			var concept string
			if err := conceptRows.Scan(&concept); err != nil {
				_ = conceptRows.Close()
				return nil, fmt.Errorf("failed to scan concept: %w", err)
			}
			l.Concepts = append(l.Concepts, concept)
		}
		_ = conceptRows.Close()

		learnings = append(learnings, l)
	}

	return learnings, nil
}

// GetRelatedConcepts returns concepts that match keywords in a task's title/description.
// Matches are case-insensitive and ranked by learning count.
func (db *DB) GetRelatedConcepts(taskID string) ([]model.Concept, error) {
	// Get task details
	item, err := db.GetItem(taskID)
	if err != nil {
		return nil, err
	}

	// Concepts are global, so a task can surface knowledge learned elsewhere.
	concepts, err := db.ListConcepts(false)
	if err != nil {
		return nil, err
	}

	if len(concepts) == 0 {
		return nil, nil
	}

	// Build search text from title and description
	searchText := strings.ToLower(item.Title + " " + item.Description)

	// Filter concepts whose name appears in the search text
	// Only include concepts that have at least one learning
	var related []model.Concept
	for _, c := range concepts {
		if c.LearningCount > 0 && strings.Contains(searchText, strings.ToLower(c.Name)) {
			related = append(related, c)
		}
	}

	return related, nil
}
