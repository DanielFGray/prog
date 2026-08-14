package main

import (
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func TestReflectionLinksLearningToCompletedTask(t *testing.T) {
	output := captureOutput(func() {
		printReflection("ts-done01")
	})
	if !strings.Contains(output, "--task ts-done01") {
		t.Fatalf("reflection output %q does not link the completed task", output)
	}
}

func TestLearningTaskIDUsesExplicitCompletedTask(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	completed := &model.Item{
		ID: "ts-done01", Project: "test", Type: model.ItemTypeTask,
		Title: "Completed task", Status: model.StatusDone, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateItem(completed); err != nil {
		t.Fatalf("create item: %v", err)
	}

	taskID, err := learningTaskID(database, completed.ID)
	if err != nil {
		t.Fatalf("resolve learning task: %v", err)
	}
	if taskID == nil || *taskID != completed.ID {
		t.Fatalf("task ID = %v, want %s", taskID, completed.ID)
	}
}

func TestLearningTaskIDLeavesTaskOmitted(t *testing.T) {
	database := setupTestDB(t)
	taskID, err := learningTaskID(database, "")
	if err != nil {
		t.Fatalf("resolve omitted task: %v", err)
	}
	if taskID != nil {
		t.Fatalf("task ID = %v, want nil", taskID)
	}
}
