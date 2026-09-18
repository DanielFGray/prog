package db

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

// setupV3DB builds a database frozen at schema version 3, where learnings and
// concepts still carry a project column and a concept name is unique only
// within a project.
func setupV3DB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v3.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(baseSchema); err != nil {
		t.Fatalf("failed to create base schema: %v", err)
	}
	// migrations[0] and [1] upgrade to v2 and v3.
	for i, m := range migrations[:2] {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to apply migration to v%d: %v", i+2, err)
		}
	}
	if err := db.setSchemaVersion(3); err != nil {
		t.Fatalf("failed to set schema version: %v", err)
	}
	return db
}

// setupV4DB builds a database frozen at schema version 4, where concepts are
// already global but learnings.task_id still references items with no delete
// behavior.
func setupV4DB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v4.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(baseSchema); err != nil {
		t.Fatalf("failed to create base schema: %v", err)
	}
	// migrations[0], [1], and [2] upgrade to v2, v3, and v4.
	for i, m := range migrations[:3] {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to apply migration to v%d: %v", i+2, err)
		}
	}
	if err := db.setSchemaVersion(4); err != nil {
		t.Fatalf("failed to set schema version: %v", err)
	}
	return db
}

// setupV5DB builds a database frozen at schema version 5, where learnings still
// store file paths as a JSON array in the files column.
func setupV5DB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v5.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(baseSchema); err != nil {
		t.Fatalf("failed to create base schema: %v", err)
	}
	for i, m := range migrations[:4] {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to apply migration to v%d: %v", i+2, err)
		}
	}
	if err := db.setSchemaVersion(5); err != nil {
		t.Fatalf("failed to set schema version: %v", err)
	}
	return db
}

// TestMigrateV4_MergesProjectScopedConcepts is the regression test for the move
// to global knowledge. It builds a v3 dataset where the same concept name lives
// in three projects with different summaries and timestamps, and where one
// learning is linked to two of those duplicates, then checks that the merge
// loses nothing.
func TestMigrateV4_MergesProjectScopedConcepts(t *testing.T) {
	db := setupV3DB(t)

	concepts := []struct {
		id, name, project string
		summary           any
		lastUpdated       string
	}{
		// "auth" exists in three projects. The newest row has a summary, so it
		// supplies both the surviving id and the surviving summary.
		{"con-auth01", "auth", "alpha", "", "2024-01-01 00:00:00"},
		{"con-auth02", "auth", "beta", "Beta auth summary", "2024-03-01 00:00:00"},
		{"con-auth03", "auth", "gamma", "Gamma auth summary", "2024-02-01 00:00:00"},
		// "caching" splits the two: the newest row has no summary at all, so the
		// surviving id and the surviving summary come from different rows.
		{"con-cache1", "caching", "alpha", nil, "2024-01-05 00:00:00"},
		{"con-cache2", "caching", "beta", "Cache notes", "2024-01-04 00:00:00"},
		// "solo" has no duplicate and must survive untouched.
		{"con-solo01", "solo", "alpha", "Solo summary", "2024-01-06 00:00:00"},
	}
	for _, c := range concepts {
		if _, err := db.Exec(
			`INSERT INTO concepts (id, name, project, summary, last_updated) VALUES (?, ?, ?, ?, ?)`,
			c.id, c.name, c.project, c.summary, c.lastUpdated,
		); err != nil {
			t.Fatalf("failed to insert concept %s: %v", c.id, err)
		}
	}

	learnings := []struct{ id, project, summary string }{
		{"lrn-alpha1", "alpha", "Alpha learning about tokens"},
		{"lrn-beta01", "beta", "Beta learning about tokens"},
		{"lrn-gamma1", "gamma", "Gamma learning about caches"},
	}
	for _, l := range learnings {
		if _, err := db.Exec(
			`INSERT INTO learnings (id, project, summary, detail, files, status)
			 VALUES (?, ?, ?, '', '[]', 'active')`,
			l.id, l.project, l.summary,
		); err != nil {
			t.Fatalf("failed to insert learning %s: %v", l.id, err)
		}
	}

	links := [][2]string{
		{"lrn-alpha1", "con-auth01"},
		{"lrn-alpha1", "con-auth03"}, // collapses onto the same link as the row above
		{"lrn-alpha1", "con-cache1"},
		{"lrn-beta01", "con-auth02"},
		{"lrn-beta01", "con-cache2"},
		{"lrn-gamma1", "con-auth03"},
		{"lrn-gamma1", "con-solo01"},
	}
	for _, l := range links {
		if _, err := db.Exec(
			`INSERT INTO learning_concepts (learning_id, concept_id) VALUES (?, ?)`, l[0], l[1],
		); err != nil {
			t.Fatalf("failed to link %s -> %s: %v", l[0], l[1], err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration to v4 failed: %v", err)
	}

	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema version = %d, want %d", version, SchemaVersion)
	}

	// One concept per name, no project column.
	if cols := tableColumns(t, db, "concepts"); hasColumn(cols, "project") {
		t.Errorf("concepts still has a project column: %v", cols)
	}
	if cols := tableColumns(t, db, "learnings"); hasColumn(cols, "project") {
		t.Errorf("learnings still has a project column: %v", cols)
	}

	got, err := db.ListConcepts(false)
	if err != nil {
		t.Fatalf("failed to list concepts: %v", err)
	}
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	sort.Strings(names)
	want := []string{"auth", "caching", "solo"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("concepts = %v, want %v", names, want)
	}

	byName := map[string]struct {
		id, summary string
		count       int
	}{}
	for _, c := range got {
		byName[c.Name] = struct {
			id, summary string
			count       int
		}{c.ID, c.Summary, c.LearningCount}
	}

	if s := byName["auth"].summary; s != "Beta auth summary" {
		t.Errorf("auth summary = %q, want the newest non-empty summary", s)
	}
	if id := byName["auth"].id; id != "con-auth02" {
		t.Errorf("auth id = %q, want the newest duplicate's id con-auth02", id)
	}
	// The newest "caching" row has no summary, so the older one supplies it.
	if s := byName["caching"].summary; s != "Cache notes" {
		t.Errorf("caching summary = %q, want %q", s, "Cache notes")
	}
	if id := byName["caching"].id; id != "con-cache1" {
		t.Errorf("caching id = %q, want the newest duplicate's id con-cache1", id)
	}
	if s := byName["solo"].summary; s != "Solo summary" {
		t.Errorf("solo summary = %q, want %q", s, "Solo summary")
	}

	if ts := conceptTimestamp(t, db, "auth"); !ts.Equal(mustParseTime(t, "2024-03-01 00:00:00")) {
		t.Errorf("auth last_updated = %v, want the newest duplicate's", ts)
	}
	if ts := conceptTimestamp(t, db, "caching"); !ts.Equal(mustParseTime(t, "2024-01-05 00:00:00")) {
		t.Errorf("caching last_updated = %v, want the newest duplicate's", ts)
	}

	// Every distinct association survives, and the duplicate pair collapsed.
	wantLinks := map[string][]string{
		"lrn-alpha1": {"auth", "caching"},
		"lrn-beta01": {"auth", "caching"},
		"lrn-gamma1": {"auth", "solo"},
	}
	for id, wantConcepts := range wantLinks {
		l, err := db.GetLearning(id)
		if err != nil {
			t.Fatalf("failed to get learning %s: %v", id, err)
		}
		sort.Strings(l.Concepts)
		if strings.Join(l.Concepts, ",") != strings.Join(wantConcepts, ",") {
			t.Errorf("learning %s concepts = %v, want %v", id, l.Concepts, wantConcepts)
		}
	}
	if n := countRows(t, db, "learning_concepts"); n != 6 {
		t.Errorf("learning_concepts rows = %d, want 6 (one duplicate pair collapsed)", n)
	}
	if byName["auth"].count != 3 {
		t.Errorf("auth learning count = %d, want 3", byName["auth"].count)
	}

	// Names are now globally unique.
	_, err = db.Exec(`INSERT INTO concepts (id, name, last_updated) VALUES ('con-dupdup', 'auth', '2024-04-01 00:00:00')`)
	if err == nil {
		t.Error("expected UNIQUE(name) to reject a second concept named auth")
	}

	// The obsolete project index is gone.
	var idx int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_learnings_project'`,
	).Scan(&idx); err != nil {
		t.Fatalf("failed to check indexes: %v", err)
	}
	if idx != 0 {
		t.Error("idx_learnings_project should have been dropped")
	}

	// Retrieval crosses the former project boundaries.
	all, err := db.GetAllLearnings(false)
	if err != nil {
		t.Fatalf("failed to get all learnings: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("learnings = %d, want 3 across all former projects", len(all))
	}
	byConcept, err := db.GetLearningsByConcepts([]string{"auth"}, false)
	if err != nil {
		t.Fatalf("failed to get learnings by concept: %v", err)
	}
	if len(byConcept) != 3 {
		t.Errorf("auth learnings = %d, want 3 across all former projects", len(byConcept))
	}
	// FTS survived the column drop.
	found, err := db.SearchLearnings("tokens", false)
	if err != nil {
		t.Fatalf("failed to search learnings: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("search hits = %d, want 2", len(found))
	}
}

// TestMigrateV4_EmptySchema covers the fresh-install path, where Init runs the
// base v1 schema and then every migration over empty tables.
func TestMigrateV4_EmptySchema(t *testing.T) {
	db := setupTestDB(t)

	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema version = %d, want %d", version, SchemaVersion)
	}
	for _, table := range []string{"concepts", "learnings"} {
		if cols := tableColumns(t, db, table); hasColumn(cols, "project") {
			t.Errorf("%s still has a project column: %v", table, cols)
		}
	}
	if err := db.EnsureConcept("auth"); err != nil {
		t.Fatalf("failed to ensure concept: %v", err)
	}
	if err := db.EnsureConcept("auth"); err != nil {
		t.Fatalf("re-ensuring a concept should be a no-op: %v", err)
	}
	got, err := db.ListConcepts(false)
	if err != nil {
		t.Fatalf("failed to list concepts: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("concepts = %d, want 1", len(got))
	}
}

// TestMigrateV5_PreservesLinkedLearnings covers the v5 upgrade, which rebuilds
// learnings so that deleting a task clears the learning's task link instead of
// failing on the foreign key. A v4 database holding a task-linked learning must
// come out of the migration with the learning, its concepts, and its link
// intact, searchable through FTS, and with the new delete behavior active.
func TestMigrateV5_PreservesLinkedLearnings(t *testing.T) {
	db := setupV4DB(t)

	task := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Task that will be deleted",
		Status:    model.StatusInProgress,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	now := time.Now()
	learningID := model.GenerateLearningID()
	conceptID := model.GenerateConceptID()
	if _, err := db.Exec(
		`INSERT INTO concepts (id, name, last_updated) VALUES (?, ?, ?)`,
		conceptID, "auth", now,
	); err != nil {
		t.Fatalf("failed to insert concept: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO learnings (id, created_at, updated_at, task_id, summary, detail, files, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		learningID, now, now, task.ID,
		"Token refresh has race condition", "Retry with exponential backoff",
		"[]", model.LearningStatusActive,
	); err != nil {
		t.Fatalf("failed to insert learning: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO learning_concepts (learning_id, concept_id) VALUES (?, ?)`,
		learningID, conceptID,
	); err != nil {
		t.Fatalf("failed to link concept: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration to v5 failed: %v", err)
	}

	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema version = %d, want %d", version, SchemaVersion)
	}

	// The learning and its task link survive the rebuild.
	got, err := db.GetLearning(learningID)
	if err != nil {
		t.Fatalf("learning lost in migration: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Errorf("taskID = %v, want %q preserved through migration", got.TaskID, task.ID)
	}
	if got.Summary != "Token refresh has race condition" {
		t.Errorf("summary = %q, want Token refresh has race condition", got.Summary)
	}
	if len(got.Concepts) != 1 || got.Concepts[0] != "auth" {
		t.Errorf("concepts = %v, want the concept link to survive", got.Concepts)
	}

	// The FTS index was rebuilt against the new rowids.
	hits, err := db.SearchLearnings("backoff", false)
	if err != nil {
		t.Fatalf("failed to search learnings: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != learningID {
		t.Errorf("search hits = %v, want the migrated learning", hits)
	}

	// Rebuilding learning_concepts must not lose its non-PK index, which the
	// base schema and the v4 migration both create.
	if idx := indexCount(t, db, "learning_concepts", "idx_learning_concepts_concept"); idx != 1 {
		t.Errorf("idx_learning_concepts_concept count = %d, want 1", idx)
	}
	if idx := indexCount(t, db, "learnings", "idx_learnings_task"); idx != 1 {
		t.Errorf("idx_learnings_task count = %d, want 1", idx)
	}
	if idx := indexCount(t, db, "learnings", "idx_learnings_status"); idx != 1 {
		t.Errorf("idx_learnings_status count = %d, want 1", idx)
	}

	// The new delete behavior is active: deleting the task clears the link.
	if err := db.DeleteItem(task.ID); err != nil {
		t.Fatalf("failed to delete task after v5 migration: %v", err)
	}
	got, err = db.GetLearning(learningID)
	if err != nil {
		t.Fatalf("learning lost with task: %v", err)
	}
	if got.TaskID != nil {
		t.Errorf("taskID = %v, want nil after task deletion", *got.TaskID)
	}
}

// TestMigrateV6_NormalizesFileEvidence covers the v6 upgrade: files JSON becomes
// learning_sources rows, the files column is removed, and evidence survives task
// deletion (learnings stay; only the task link clears).
func TestMigrateV6_NormalizesFileEvidence(t *testing.T) {
	db := setupV5DB(t)

	task := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Task that will be deleted",
		Status:    model.StatusInProgress,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	now := time.Now()
	learningID := model.GenerateLearningID()
	if _, err := db.Exec(
		`INSERT INTO learnings (id, created_at, updated_at, task_id, summary, detail, files, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		learningID, now, now, task.ID,
		"File evidence survives", "Detail about sources",
		`["internal/db/learnings.go","cmd/prog/main.go","internal/db/learnings.go"]`,
		model.LearningStatusActive,
	); err != nil {
		t.Fatalf("failed to insert learning with files JSON: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration to v6 failed: %v", err)
	}

	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema version = %d, want %d", version, SchemaVersion)
	}

	var filesCol int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('learnings') WHERE name = 'files'
	`).Scan(&filesCol); err != nil {
		t.Fatalf("failed to check files column: %v", err)
	}
	if filesCol != 0 {
		t.Errorf("files column still present after v6 migration")
	}

	got, err := db.GetLearning(learningID)
	if err != nil {
		t.Fatalf("learning lost in migration: %v", err)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %d, want exactly 2 distinct rows after duplicate collapse", len(got.Sources))
	}
	paths := map[string]bool{}
	for _, s := range got.Sources {
		paths[s.Path] = true
		if s.StartLine != nil || s.EndLine != nil {
			t.Errorf("migrated source %q should be path-only", s.Path)
		}
		wantID := learningID + "/" + s.Path
		if s.ID != wantID {
			t.Errorf("source id = %q, want deterministic %q", s.ID, wantID)
		}
	}
	if len(paths) != 2 || !paths["internal/db/learnings.go"] || !paths["cmd/prog/main.go"] {
		t.Errorf("sources = %v, want both distinct legacy paths", got.Sources)
	}
	if len(got.Files) != 2 {
		t.Errorf("derived files count = %d, want 2", len(got.Files))
	}

	if err := db.DeleteItem(task.ID); err != nil {
		t.Fatalf("failed to delete task after v6 migration: %v", err)
	}
	got, err = db.GetLearning(learningID)
	if err != nil {
		t.Fatalf("learning lost with task: %v", err)
	}
	if got.TaskID != nil {
		t.Errorf("taskID = %v, want nil after task deletion", *got.TaskID)
	}
	if len(got.Sources) != 2 {
		t.Errorf("sources after task delete = %d, want 2", len(got.Sources))
	}
}

// setupV6DB builds a database frozen at schema version 6 (normalized
// learning_sources, no learning_relations table yet).
func setupV6DB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v6.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(baseSchema); err != nil {
		t.Fatalf("failed to create base schema: %v", err)
	}
	for i, m := range migrations[:5] {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("failed to apply migration to v%d: %v", i+2, err)
		}
	}
	if err := db.setSchemaVersion(6); err != nil {
		t.Fatalf("failed to set schema version: %v", err)
	}
	return db
}

// TestMigrateV7_CreatesLearningRelations covers the v7 upgrade: learning_relations
// appears with source/target FKs, kind check, self-link rejection, and uniqueness.
func TestMigrateV7_CreatesLearningRelations(t *testing.T) {
	db := setupV6DB(t)

	var existsBefore int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='learning_relations'
	`).Scan(&existsBefore); err != nil {
		t.Fatalf("check table before migrate: %v", err)
	}
	if existsBefore != 0 {
		t.Fatalf("learning_relations unexpectedly present before v7")
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration to v7 failed: %v", err)
	}

	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema version = %d, want %d", version, SchemaVersion)
	}

	var existsAfter int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='learning_relations'
	`).Scan(&existsAfter); err != nil {
		t.Fatalf("check table after migrate: %v", err)
	}
	if existsAfter != 1 {
		t.Fatalf("learning_relations missing after v7 migration")
	}

	cols := tableColumns(t, db, "learning_relations")
	for _, want := range []string{"source_id", "target_id", "kind"} {
		if !hasColumn(cols, want) {
			t.Errorf("learning_relations missing column %q (got %v)", want, cols)
		}
	}

	old := &model.Learning{
		ID:        model.GenerateLearningID(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Summary:   "pre-v7 old",
		Status:    model.LearningStatusActive,
	}
	neu := &model.Learning{
		ID:        model.GenerateLearningID(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Summary:   "pre-v7 new",
		Status:    model.LearningStatusActive,
	}
	mustCreateLearning(t, db, old)
	mustCreateLearning(t, db, neu)

	if err := db.SupersedeLearning(neu.ID, old.ID); err != nil {
		t.Fatalf("supersede after v7 migrate: %v", err)
	}

	// Self-link rejected by CHECK constraint at the SQL layer.
	if _, err := db.Exec(`
		INSERT INTO learning_relations (source_id, target_id, kind)
		VALUES (?, ?, 'supersedes')
	`, neu.ID, neu.ID); err == nil {
		t.Fatal("expected self-link CHECK to reject insert")
	}

	// Uniqueness on (source, target, kind).
	if _, err := db.Exec(`
		INSERT INTO learning_relations (source_id, target_id, kind)
		VALUES (?, ?, 'supersedes')
	`, neu.ID, old.ID); err == nil {
		t.Fatal("expected unique constraint to reject duplicate edge")
	}

	// Only supersedes kind is allowed.
	other := &model.Learning{
		ID:        model.GenerateLearningID(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Summary:   "other",
		Status:    model.LearningStatusActive,
	}
	mustCreateLearning(t, db, other)
	if _, err := db.Exec(`
		INSERT INTO learning_relations (source_id, target_id, kind)
		VALUES (?, ?, 'supports')
	`, neu.ID, other.ID); err == nil {
		t.Fatal("expected kind CHECK to reject non-supersedes")
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	db := setupTestDB(t)
	original := migrations
	migrations = append(append([]string(nil), migrations...), `
CREATE TABLE migration_probe (id INTEGER);
INSERT INTO table_that_does_not_exist VALUES (1);
`)
	t.Cleanup(func() { migrations = original })

	if err := db.Migrate(); err == nil {
		t.Fatal("expected migration failure")
	}
	if exists, err := db.tableExists("migration_probe"); err != nil {
		t.Fatalf("check rollback table: %v", err)
	} else if exists {
		t.Fatal("failed migration left a partially created table")
	}
	version, err := db.getSchemaVersion()
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
}

func tableColumns(t *testing.T, db *DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("failed to read columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("failed to scan column name: %v", err)
		}
		cols = append(cols, name)
	}
	return cols
}

// conceptTimestamp reads a concept's last_updated. The column is declared
// DATETIME, so the driver hands it back as a time and the caller compares
// against an instant rather than the stored text.
func conceptTimestamp(t *testing.T, db *DB, name string) time.Time {
	t.Helper()
	var ts time.Time
	if err := db.QueryRow(`SELECT last_updated FROM concepts WHERE name = ?`, name).Scan(&ts); err != nil {
		if err == sql.ErrNoRows {
			t.Fatalf("concept %s not found", name)
		}
		t.Fatalf("failed to read last_updated for %s: %v", name, err)
	}
	return ts
}

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("failed to count %s: %v", table, err)
	}
	return n
}

// indexCount reports whether an index with the given name exists on a table.
// Dropping and rebuilding a table silently drops its indexes, so a migration
// that rebuilds a table must recreate each of them or lookups lose their plan.
func indexCount(t *testing.T, db *DB, table, name string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=? AND tbl_name=?`,
		name, table,
	).Scan(&n); err != nil {
		t.Fatalf("failed to count index %s on %s: %v", name, table, err)
	}
	return n
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		t.Fatalf("bad test timestamp %q: %v", s, err)
	}
	return ts
}

func hasColumn(cols []string, name string) bool {
	for _, s := range cols {
		if s == name {
			return true
		}
	}
	return false
}
