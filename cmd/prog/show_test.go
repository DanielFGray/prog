package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func TestPrintItemDetail_CollapsesRepeatedLogs(t *testing.T) {
	database := setupTestDB(t)

	item := &model.Item{
		ID: "ts-repeat1", Project: "test", Type: model.ItemTypeTask,
		Title: "Repeated Logs", Status: model.StatusOpen,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := database.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := database.AddLog(item.ID, "still working"); err != nil {
			t.Fatalf("failed to add log: %v", err)
		}
	}
	if err := database.AddLog(item.ID, "real progress"); err != nil {
		t.Fatalf("failed to add log: %v", err)
	}
	if err := database.AddLog(item.ID, "still working"); err != nil {
		t.Fatalf("failed to add log: %v", err)
	}

	logs, err := database.GetLogs(item.ID)
	if err != nil {
		t.Fatalf("failed to get logs: %v", err)
	}

	output := captureOutput(func() {
		printItemDetail(item, logs, nil, nil)
	})

	if !strings.Contains(output, "still working (x5)") {
		t.Errorf("expected collapsed run 'still working (x5)', output:\n%s", output)
	}
	if !strings.Contains(output, "real progress") {
		t.Errorf("expected interleaved entry 'real progress', output:\n%s", output)
	}
	if strings.Count(output, "still working") != 2 {
		t.Errorf("expected exactly two 'still working' lines (collapsed runs), output:\n%s", output)
	}
}

func TestPrintItemDetail_NoCountForSingleLog(t *testing.T) {
	database := setupTestDB(t)

	item := &model.Item{
		ID: "ts-single1", Project: "test", Type: model.ItemTypeTask,
		Title: "Single Log", Status: model.StatusOpen,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := database.CreateItem(item); err != nil {
		t.Fatalf("failed to create item: %v", err)
	}
	if err := database.AddLog(item.ID, "one entry"); err != nil {
		t.Fatalf("failed to add log: %v", err)
	}

	logs, err := database.GetLogs(item.ID)
	if err != nil {
		t.Fatalf("failed to get logs: %v", err)
	}

	output := captureOutput(func() {
		printItemDetail(item, logs, nil, nil)
	})

	if strings.Contains(output, "(x1)") {
		t.Errorf("single log should not show a count, output:\n%s", output)
	}
	if !strings.Contains(output, "one entry") {
		t.Errorf("missing log entry, output:\n%s", output)
	}
}

func TestPrintItemDetail_AlwaysShowsTaskContextCommands(t *testing.T) {
	item := &model.Item{ID: "ts-context1", Title: "Context task"}

	output := captureOutput(func() {
		printItemDetail(item, nil, nil, nil)
	})

	if !strings.Contains(output, "prog context --task ts-context1") {
		t.Errorf("missing task context command, output:\n%s", output)
	}
	if !strings.Contains(output, "prog context --task ts-context1 --summary") {
		t.Errorf("missing summary context command, output:\n%s", output)
	}
}

func TestPrintItemDetail_ShowsConceptFallback(t *testing.T) {
	item := &model.Item{ID: "ts-context2", Title: "Concept task"}
	concepts := []model.Concept{{Name: "database", Summary: "Storage patterns", LearningCount: 3}}

	output := captureOutput(func() {
		printItemDetail(item, nil, nil, concepts)
	})

	if !strings.Contains(output, "Suggested concepts:") {
		t.Errorf("missing suggested concepts heading, output:\n%s", output)
	}
	if !strings.Contains(output, "Load by concept: prog context -c database --summary") {
		t.Errorf("missing concept fallback command, output:\n%s", output)
	}
}

func TestItemContextJSON(t *testing.T) {
	concepts := []model.Concept{{Name: "database"}}

	got := itemContextJSON("ts-context3", concepts)
	if got.TaskCommand != "prog context --task ts-context3" {
		t.Errorf("task command = %q", got.TaskCommand)
	}
	if got.SummaryCommand != "prog context --task ts-context3 --summary" {
		t.Errorf("summary command = %q", got.SummaryCommand)
	}
	if got.ConceptCommand != "prog context -c database --summary" {
		t.Errorf("concept command = %q", got.ConceptCommand)
	}
}

func TestItemShowJSON_IncludesContextGuidance(t *testing.T) {
	concepts := []model.Concept{{Name: "database", Summary: "Storage patterns", LearningCount: 2}}
	out := ItemShowJSON{
		ID:                "ts-context4",
		SuggestedConcepts: conceptsToJSON(concepts),
		Context:           itemContextJSON("ts-context4", concepts),
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ItemShowJSON
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SuggestedConcepts) != 1 || got.SuggestedConcepts[0].Name != "database" {
		t.Errorf("suggested concepts = %+v", got.SuggestedConcepts)
	}
	if got.Context.TaskCommand != "prog context --task ts-context4" {
		t.Errorf("task command = %q", got.Context.TaskCommand)
	}
}
