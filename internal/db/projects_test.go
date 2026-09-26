package db

import (
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func TestEnsureProject(t *testing.T) {
	db := setupTestDB(t)

	// First call should create the project
	err := db.EnsureProject("myproject")
	if err != nil {
		t.Fatalf("failed to ensure project: %v", err)
	}

	// Second call should be idempotent (no error)
	err = db.EnsureProject("myproject")
	if err != nil {
		t.Fatalf("failed on second ensure: %v", err)
	}

	// Project should appear in list
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	if len(projects) != 1 || projects[0] != "myproject" {
		t.Errorf("expected [myproject], got %v", projects)
	}
}

func TestListProjectsEmpty(t *testing.T) {
	db := setupTestDB(t)

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}

	if len(projects) != 0 {
		t.Errorf("expected empty list, got %v", projects)
	}
}

func TestRenameProject(t *testing.T) {
	db := setupTestDB(t)

	// Create old project with an item
	err := db.EnsureProject("old-name")
	if err != nil {
		t.Fatalf("failed to ensure project: %v", err)
	}
	item := &model.Item{
		ID:        "ts-test123",
		Project:   "old-name",
		Type:      "task",
		Title:     "Test item",
		Status:    "open",
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Rename project
	err = db.RenameProject("old-name", "new-name")
	if err != nil {
		t.Fatalf("failed to rename project: %v", err)
	}

	// Old project should not exist
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p == "old-name" {
			found = true
		}
	}
	if found {
		t.Error("old project name should not exist")
	}

	// New project should exist
	found = false
	for _, p := range projects {
		if p == "new-name" {
			found = true
		}
	}
	if !found {
		t.Error("new project name should exist")
	}

	// Item should be in new project
	items, err := db.ListItemsFiltered(ListFilter{Project: "new-name"})
	if err != nil {
		t.Fatalf("failed to list items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item in new-name, got %d", len(items))
	}
}

func TestRenameProjectNotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.RenameProject("nonexistent", "new-name")
	if err == nil {
		t.Error("expected error for non-existent project")
	}
}

func TestRenameProjectMerge(t *testing.T) {
	db := setupTestDB(t)

	// Create two projects with items
	err := db.EnsureProject("project1")
	if err != nil {
		t.Fatalf("failed to ensure project1: %v", err)
	}
	err = db.EnsureProject("project2")
	if err != nil {
		t.Fatalf("failed to ensure project2: %v", err)
	}

	// Add item to project1
	item1 := &model.Item{
		ID:        "ts-merge1",
		Project:   "project1",
		Type:      "task",
		Title:     "Item from project1",
		Status:    "open",
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item1); err != nil {
		t.Fatalf("failed to create item1: %v", err)
	}

	// Add item to project2
	item2 := &model.Item{
		ID:        "ts-merge2",
		Project:   "project2",
		Type:      "task",
		Title:     "Item from project2",
		Status:    "open",
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item2); err != nil {
		t.Fatalf("failed to create item2: %v", err)
	}

	// Merge project1 into project2
	err = db.RenameProject("project1", "project2")
	if err != nil {
		t.Fatalf("failed to merge project: %v", err)
	}

	// project1 should not exist
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	for _, p := range projects {
		if p == "project1" {
			t.Error("project1 should not exist after merge")
		}
	}

	// Both items should now be in project2
	items, err := db.ListItemsFiltered(ListFilter{Project: "project2"})
	if err != nil {
		t.Fatalf("failed to list items: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items in project2 after merge, got %d", len(items))
	}
}

// TestRenameProjectLeavesKnowledgeAlone pins the consequence of making
// knowledge global: a project rename touches items and labels only, and the
// learnings and concepts recorded while that project existed stay reachable
// under their own names.
func TestRenameProjectLeavesKnowledgeAlone(t *testing.T) {
	db := setupTestDB(t)

	if err := db.EnsureProject("old-name"); err != nil {
		t.Fatalf("failed to ensure project: %v", err)
	}

	now := time.Now()
	learning := &model.Learning{
		ID:        model.GenerateLearningID(),
		CreatedAt: now,
		UpdatedAt: now,
		Summary:   "Knowledge outlives the project it was found in",
		Status:    model.LearningStatusActive,
		Concepts:  []string{"auth"},
	}
	if err := db.CreateLearning(learning); err != nil {
		t.Fatalf("failed to create learning: %v", err)
	}

	if err := db.RenameProject("old-name", "new-name"); err != nil {
		t.Fatalf("failed to rename project: %v", err)
	}

	concepts, err := db.ListConcepts(false)
	if err != nil {
		t.Fatalf("failed to list concepts: %v", err)
	}
	if len(concepts) != 1 || concepts[0].Name != "auth" {
		t.Fatalf("concepts = %v, want a single concept named auth", concepts)
	}
	if concepts[0].LearningCount != 1 {
		t.Errorf("auth learning count = %d, want 1", concepts[0].LearningCount)
	}

	found, err := db.GetLearningsByConcepts([]string{"auth"}, false)
	if err != nil {
		t.Fatalf("failed to get learnings by concept: %v", err)
	}
	if len(found) != 1 || found[0].ID != learning.ID {
		t.Errorf("learnings = %v, want the one created before the rename", found)
	}
}

// Regression: `prog add -p " spindle"` stored the project verbatim, so
// `prog list -p spindle` returned nothing even though show printed "spindle".
func TestCreateItemNormalizesProjectForFiltering(t *testing.T) {
	db := setupTestDB(t)

	item := &model.Item{
		ID:        model.GenerateID(model.ItemTypeTask),
		Project:   " brandnew\t",
		Type:      model.ItemTypeTask,
		Title:     "task in a brand-new project",
		Status:    model.StatusOpen,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	items, err := db.ListItemsFiltered(ListFilter{Project: "brandnew"})
	if err != nil {
		t.Fatalf("failed to list items: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("list -p brandnew: got %d items, want [%s]", len(items), item.ID)
	}

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	if len(projects) != 1 || projects[0] != "brandnew" {
		t.Errorf("projects = %q, want [brandnew]", projects)
	}
}

// Existing rows written before normalization must become filterable after
// the startup migration, without a manual step. Seeds at v7 so only the
// trim-whitespace migration (v8) reruns: earlier migrations rebuild tables
// and are not idempotent.
func TestMigrateTrimsExistingProjectNames(t *testing.T) {
	db := setupTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO projects (name) VALUES (' spindle'), ('spindle')`,
		`INSERT INTO items (id, project, type, title, description, status) VALUES
			('ep-old', ' spindle', 'epic', 'old epic', '', 'open'),
			('ts-new', 'spindle', 'task', 'new task', '', 'open')`,
		`PRAGMA user_version = 7`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent: a second startup is a no-op.
	if err := db.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	items, err := db.ListItemsFiltered(ListFilter{Project: "spindle"})
	if err != nil {
		t.Fatalf("failed to list items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list -p spindle: got %d items, want 2", len(items))
	}

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	if len(projects) != 1 || projects[0] != "spindle" {
		t.Errorf("projects = %q, want [spindle]", projects)
	}
}
