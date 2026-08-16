package main

import (
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
