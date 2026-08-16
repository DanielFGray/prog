package model

import (
	"testing"
	"time"
)

func TestCollapseLogs(t *testing.T) {
	base := time.Date(2026, 8, 16, 8, 46, 0, 0, time.UTC)
	logs := []Log{
		{Message: "claimed", CreatedAt: base},
		{Message: "working on it", CreatedAt: base.Add(time.Minute)},
		{Message: "working on it", CreatedAt: base.Add(2 * time.Minute)},
		{Message: "working on it", CreatedAt: base.Add(3 * time.Minute)},
		{Message: "done", CreatedAt: base.Add(4 * time.Minute)},
		{Message: "working on it", CreatedAt: base.Add(5 * time.Minute)},
	}

	got := CollapseLogs(logs)
	want := []CollapsedLog{
		{Message: "claimed", CreatedAt: base, Count: 1},
		{Message: "working on it", CreatedAt: base.Add(time.Minute), Count: 3},
		{Message: "done", CreatedAt: base.Add(4 * time.Minute), Count: 1},
		{Message: "working on it", CreatedAt: base.Add(5 * time.Minute), Count: 1},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCollapseLogs_Empty(t *testing.T) {
	got := CollapseLogs(nil)
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}
