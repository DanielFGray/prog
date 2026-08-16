package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	if err := db.Init(); err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Should create parent directories
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Error("expected directory to be created")
	}
}

// TestPoolConnectionsEnforceForeignKeys is the regression for connection-local
// pragmas dropping off pooled connections. Open() used to run PRAGMA
// foreign_keys = ON on one connection, but database/sql opens new connections
// lazily, and a connection that never ran the pragma starts with foreign keys
// off. Every pooled connection must enforce them.
func TestPoolConnectionsEnforceForeignKeys(t *testing.T) {
	db := setupTestDB(t)

	// Force every Conn() to open a brand-new physical connection. With a single
	// open slot and zero idle slots, a returned connection is destroyed instead
	// of reused, so the connection that setupTestDB exercised cannot serve this
	// test's inserts.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	parent := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "parent",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(parent); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Drain the connection CreateItem used so the next Conn() must open fresh.
	ctx := context.Background()
	drain, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to grab connection: %v", err)
	}
	if err := drain.Close(); err != nil {
		t.Fatalf("failed to close connection: %v", err)
	}

	// This Conn() opens a fresh pooled connection. If foreign keys are not
	// applied per connection, the orphan log row is accepted.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to grab fresh connection: %v", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		"INSERT INTO logs (item_id, message) VALUES (?, ?)",
		"ts-does-not-exist", "orphan")
	if err == nil {
		t.Fatal("pooled connection accepted an orphan row; foreign_keys is not applied to every connection")
	}

	var busyTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout on pooled connection = %d, want 5000", busyTimeout)
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("failed to get default path: %v", err)
	}

	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}

	if !contains(path, ".prog/prog.db") {
		t.Errorf("expected path to contain .prog/prog.db, got %q", path)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s[1:], substr))
}

func TestCreateItem(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Test task",
		Status:    model.StatusOpen,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Verify it was created
	got, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}

	if got.Title != item.Title {
		t.Errorf("title = %q, want %q", got.Title, item.Title)
	}
	if got.Project != item.Project {
		t.Errorf("project = %q, want %q", got.Project, item.Project)
	}
}

func TestCreateItem_InvalidType(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:      "ts-123456",
		Project: "test",
		Type:    model.ItemType("invalid"),
		Title:   "Test",
		Status:  model.StatusOpen,
	}

	err := db.CreateItem(item)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestCreateItem_InvalidStatus(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:      "ts-123456",
		Project: "test",
		Type:    model.ItemTypeTask,
		Title:   "Test",
		Status:  model.Status("invalid"),
	}

	err := db.CreateItem(item)
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestGetItem_NotFound(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.GetItem("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestUpdateStatus(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Test",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	if err := db.UpdateStatus(item.ID, model.StatusInProgress); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	got, _ := db.GetItem(item.ID)
	if got.Status != model.StatusInProgress {
		t.Errorf("status = %q, want %q", got.Status, model.StatusInProgress)
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.UpdateStatus("nonexistent", model.StatusDone)
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestUpdateStatus_InvalidStatus(t *testing.T) {
	db := setupTestDB(t)

	err := db.UpdateStatus("ts-123456", model.Status("invalid"))
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestCompareAndSetStatus_Success(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        "ts-cas001",
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "CAS task",
		Status:    model.StatusInProgress,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	applied, err := db.CompareAndSetStatus(item.ID, model.StatusInProgress, model.StatusReviewing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !applied {
		t.Fatal("expected compare-and-set to apply")
	}

	got, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if got.Status != model.StatusReviewing {
		t.Errorf("status = %q, want %q", got.Status, model.StatusReviewing)
	}
}

func TestCompareAndSetStatus_Mismatch(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        "ts-cas002",
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "CAS task",
		Status:    model.StatusDone,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	applied, err := db.CompareAndSetStatus(item.ID, model.StatusInProgress, model.StatusReviewing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("expected compare-and-set not to apply when status differs")
	}

	got, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if got.Status != model.StatusDone {
		t.Errorf("status = %q, want %q (row must be left untouched)", got.Status, model.StatusDone)
	}
}

// TestCompareAndSetStatus_ConcurrentTransitionNotOverwritten is the race the
// review command used to lose: a caller reads in_progress, a concurrent
// transition lands, and the stale write then overwrites it. The compare-and-set
// must fail instead, leaving the concurrent transition intact.
func TestCompareAndSetStatus_ConcurrentTransitionNotOverwritten(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        "ts-cas003",
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "CAS task",
		Status:    model.StatusInProgress,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// The review flow reads the row and sees in_progress.
	read, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if read.Status != model.StatusInProgress {
		t.Fatalf("expected in_progress, got %s", read.Status)
	}

	// A concurrent transition marks the task done before review's write lands.
	if err := db.UpdateStatus(item.ID, model.StatusDone); err != nil {
		t.Fatalf("failed to complete item: %v", err)
	}

	// Review's stale write must fail and must not resurrect the done task.
	applied, err := db.CompareAndSetStatus(item.ID, model.StatusInProgress, model.StatusReviewing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Fatal("compare-and-set must not overwrite a concurrent transition")
	}

	got, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if got.Status != model.StatusDone {
		t.Errorf("concurrent 'done' transition was overwritten: status = %q, want %q", got.Status, model.StatusDone)
	}
}

func TestCompareAndSetStatus_NotFound(t *testing.T) {
	db := setupTestDB(t)

	applied, err := db.CompareAndSetStatus("nonexistent", model.StatusInProgress, model.StatusReviewing)
	if applied {
		t.Error("expected no update for nonexistent item")
	}
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestCompareAndSetStatus_InvalidStatus(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        "ts-cas004",
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "CAS task",
		Status:    model.StatusInProgress,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	applied, err := db.CompareAndSetStatus(item.ID, model.StatusInProgress, model.Status("invalid"))
	if applied {
		t.Error("expected no update for invalid status")
	}
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestAppendDescription(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:          model.GenerateID(model.ItemTypeTask),
		Project:     "test",
		Type:        model.ItemTypeTask,
		Title:       "Test",
		Description: "Initial",
		Status:      model.StatusOpen,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	if err := db.AppendDescription(item.ID, "Appended text"); err != nil {
		t.Fatalf("failed to append: %v", err)
	}

	got, _ := db.GetItem(item.ID)
	if got.Description == "Initial" {
		t.Error("description was not appended")
	}
}

func TestSetParent(t *testing.T) {
	db := setupTestDB(t)

	epic := &model.Item{
		ID:        model.GenerateID(model.ItemTypeEpic),
		Project:   "test",
		Type:      model.ItemTypeEpic,
		Title:     "Test Epic",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(epic); err != nil {
		t.Fatalf("failed to create epic: %v", err)
	}

	task := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Test Task",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	if err := db.SetParent(task.ID, epic.ID); err != nil {
		t.Fatalf("failed to set parent: %v", err)
	}

	got, _ := db.GetItem(task.ID)
	if got.ParentID == nil {
		t.Fatal("expected parent ID to be set")
	}
	if *got.ParentID != epic.ID {
		t.Errorf("parent = %q, want %q", *got.ParentID, epic.ID)
	}
}

func TestSetParent_NotEpic(t *testing.T) {
	db := setupTestDB(t)

	task1 := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Task 1",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task1); err != nil {
		t.Fatalf("failed to create task1: %v", err)
	}

	task2 := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Task 2",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task2); err != nil {
		t.Fatalf("failed to create task2: %v", err)
	}

	err := db.SetParent(task2.ID, task1.ID)
	if err == nil {
		t.Error("expected error when parent is not an epic")
	}
}

func TestSetDescription(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:          model.GenerateID(model.ItemTypeTask),
		Project:     "test",
		Type:        model.ItemTypeTask,
		Title:       "Test",
		Description: "Original description",
		Status:      model.StatusOpen,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	if err := db.SetDescription(item.ID, "New description"); err != nil {
		t.Fatalf("failed to set description: %v", err)
	}

	got, _ := db.GetItem(item.ID)
	if got.Description != "New description" {
		t.Errorf("description = %q, want %q", got.Description, "New description")
	}
}

func TestSetDescription_EmptyToContent(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Test",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	if err := db.SetDescription(item.ID, "Added description"); err != nil {
		t.Fatalf("failed to set description: %v", err)
	}

	got, _ := db.GetItem(item.ID)
	if got.Description != "Added description" {
		t.Errorf("description = %q, want %q", got.Description, "Added description")
	}
}

func TestSetDescription_NotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.SetDescription("nonexistent", "text")
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestSetParent_NotFound(t *testing.T) {
	db := setupTestDB(t)

	epic := &model.Item{
		ID:        model.GenerateID(model.ItemTypeEpic),
		Project:   "test",
		Type:      model.ItemTypeEpic,
		Title:     "Epic",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(epic); err != nil {
		t.Fatalf("failed to create epic: %v", err)
	}

	// Nonexistent task
	err := db.SetParent("nonexistent", epic.ID)
	if err == nil {
		t.Error("expected error for nonexistent task")
	}

	// Nonexistent parent
	task := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Task",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	err = db.SetParent(task.ID, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent parent")
	}
}

func TestCreateItem_WithDefinitionOfDone(t *testing.T) {
	db := setupTestDB(t)

	dod := "Tests pass; Docs updated"
	item := &model.Item{
		ID:               model.GenerateID(model.ItemTypeTask),
		Project:          "test",
		Type:             model.ItemTypeTask,
		Title:            "Task with DoD",
		DefinitionOfDone: &dod,
		Status:           model.StatusOpen,
		Priority:         2,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	got, err := db.GetItem(item.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}

	if got.DefinitionOfDone == nil {
		t.Fatal("expected DefinitionOfDone to be set")
	}
	if *got.DefinitionOfDone != dod {
		t.Errorf("DefinitionOfDone = %q, want %q", *got.DefinitionOfDone, dod)
	}
}

func TestSetDefinitionOfDone(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Test",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Set DoD
	dod := "All tests pass"
	if err := db.SetDefinitionOfDone(item.ID, &dod); err != nil {
		t.Fatalf("failed to set DoD: %v", err)
	}

	got, _ := db.GetItem(item.ID)
	if got.DefinitionOfDone == nil {
		t.Fatal("expected DefinitionOfDone to be set")
	}
	if *got.DefinitionOfDone != dod {
		t.Errorf("DefinitionOfDone = %q, want %q", *got.DefinitionOfDone, dod)
	}

	// Clear DoD
	if err := db.SetDefinitionOfDone(item.ID, nil); err != nil {
		t.Fatalf("failed to clear DoD: %v", err)
	}

	got, _ = db.GetItem(item.ID)
	if got.DefinitionOfDone != nil {
		t.Errorf("expected DefinitionOfDone to be nil, got %q", *got.DefinitionOfDone)
	}
}

func TestSetDefinitionOfDone_NotFound(t *testing.T) {
	db := setupTestDB(t)

	dod := "Some criteria"
	err := db.SetDefinitionOfDone("nonexistent", &dod)
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}
