package main

import (
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func TestEditItemAppliesTitleAndDefinitionOfDone(t *testing.T) {
	database := setupTestDB(t)
	item := &model.Item{
		ID:        "ts-edit01",
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Old title",
		Status:    model.StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.CreateItem(item); err != nil {
		t.Fatalf("create item: %v", err)
	}

	output := captureOutput(func() {
		edited, err := editItem(database, item.ID, "New title", true, "Tests pass", true)
		if err != nil {
			t.Fatalf("edit item: %v", err)
		}
		if !edited {
			t.Fatal("combined flags were not treated as an edit")
		}
	})

	got, err := database.GetItem(item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Title != "New title" {
		t.Errorf("title = %q, want %q", got.Title, "New title")
	}
	if got.DefinitionOfDone == nil || *got.DefinitionOfDone != "Tests pass" {
		t.Errorf("definition of done = %v, want %q", got.DefinitionOfDone, "Tests pass")
	}
	for _, message := range []string{"Updated title", "Updated definition of done"} {
		if !strings.Contains(output, message) {
			t.Errorf("output %q does not contain %q", output, message)
		}
	}
}

func TestEnsureInteractiveEditor_AllowsTTY(t *testing.T) {
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = orig })

	if err := ensureInteractiveEditor(); err != nil {
		t.Fatalf("expected nil on TTY, got %v", err)
	}
}

func TestEnsureInteractiveEditor_RefusesNonTTY(t *testing.T) {
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })

	err := ensureInteractiveEditor()
	if err == nil {
		t.Fatal("expected error when stdin is not a terminal")
	}
	msg := err.Error()
	for _, want := range []string{
		"stdin is not a terminal",
		`prog desc <id>`,
		`prog append <id>`,
		`prog edit <id> --title`,
		`prog edit <id> --dod`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

func TestEditCmdHelpMentionsScriptableAlternatives(t *testing.T) {
	help := editCmd.Long
	for _, want := range []string{
		`prog desc <id>`,
		`prog append <id>`,
		"non-interactive",
		"requires a TTY",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("edit help missing %q", want)
		}
	}
}
