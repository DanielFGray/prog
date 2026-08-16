package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func newReviewTask(t *testing.T, id string, status model.Status) *model.Item {
	t.Helper()
	return &model.Item{
		ID:        id,
		Project:   "test",
		Type:      model.ItemTypeTask,
		Title:     "Review test task",
		Status:    status,
		Priority:  2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestReviewCmd_TransitionsFromInProgress(t *testing.T) {
	database := setupTestDB(t)

	task := newReviewTask(t, "ts-rev001", model.StatusInProgress)
	if err := database.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Simulate what reviewCmd does: compare-and-set in_progress -> reviewing.
	applied, err := database.CompareAndSetStatus(task.ID, model.StatusInProgress, model.StatusReviewing)
	if err != nil {
		t.Fatalf("failed to transition: %v", err)
	}
	if !applied {
		t.Fatal("expected review to apply to an in_progress task")
	}

	got, err := database.GetItem(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if got.Status != model.StatusReviewing {
		t.Errorf("status = %q, want %q", got.Status, model.StatusReviewing)
	}
}

func TestReviewCmd_RejectsNonInProgressStatuses(t *testing.T) {
	database := setupTestDB(t)

	statuses := []model.Status{
		model.StatusDraft,
		model.StatusOpen,
		model.StatusBlocked,
		model.StatusReviewing,
		model.StatusDone,
		model.StatusCanceled,
	}
	for i, status := range statuses {
		task := newReviewTask(t, fmt.Sprintf("ts-rev0%d", i+2), status)
		if err := database.CreateItem(task); err != nil {
			t.Fatalf("failed to create task: %v", err)
		}

		applied, err := database.CompareAndSetStatus(task.ID, model.StatusInProgress, model.StatusReviewing)
		if err != nil {
			t.Fatalf("unexpected error for status %s: %v", status, err)
		}
		if applied {
			t.Errorf("review must not apply to a task in %s", status)
		}

		got, err := database.GetItem(task.ID)
		if err != nil {
			t.Fatalf("failed to get task: %v", err)
		}
		if got.Status != status {
			t.Errorf("status = %q, want %q (row must be left untouched)", got.Status, status)
		}
	}
}

// TestReviewCmd_ConcurrentDoneNotOverwritten is the race the review command used
// to lose: a concurrent "done" lands after review's read but before its write,
// and the stale write resurrected the completed task. The compare-and-set write
// must fail instead.
func TestReviewCmd_ConcurrentDoneNotOverwritten(t *testing.T) {
	database := setupTestDB(t)

	task := newReviewTask(t, "ts-rev009", model.StatusInProgress)
	if err := database.CreateItem(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Review's read sees in_progress...
	item, err := database.GetItem(task.ID)
	if err != nil {
		t.Fatalf("failed to get item: %v", err)
	}
	if item.Status != model.StatusInProgress {
		t.Fatalf("expected in_progress, got %s", item.Status)
	}

	// ...but a concurrent transition completes the task before review's write.
	if err := database.UpdateStatus(task.ID, model.StatusDone); err != nil {
		t.Fatalf("failed to complete task: %v", err)
	}

	// Review's write must fail and leave the completed task alone.
	applied, err := database.CompareAndSetStatus(task.ID, model.StatusInProgress, model.StatusReviewing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Fatal("review must not resurrect a completed task")
	}

	got, err := database.GetItem(task.ID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if got.Status != model.StatusDone {
		t.Errorf("concurrent 'done' transition was overwritten: status = %q, want %q", got.Status, model.StatusDone)
	}
}
