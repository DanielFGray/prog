package main

import (
	"strings"
	"testing"
)

func TestCountProjectFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "none", args: []string{"add", "title"}, want: 0},
		{name: "short once", args: []string{"add", "title", "-p", "prog"}, want: 1},
		{name: "long once", args: []string{"add", "title", "--project", "prog"}, want: 1},
		{name: "short equals", args: []string{"add", "-p=prog", "title"}, want: 1},
		{name: "long equals", args: []string{"add", "--project=prog", "title"}, want: 1},
		{name: "attached value", args: []string{"add", "-pprog", "title"}, want: 1},
		{name: "duplicate short", args: []string{"add", "title", "-p", "prog", "-p", "1"}, want: 2},
		{name: "short then long", args: []string{"add", "-p", "prog", "--project", "other"}, want: 2},
		{name: "mistaken -priority", args: []string{"add", "title", "-p", "prog", "-priority", "1"}, want: 2},
		{name: "mistaken -parent", args: []string{"add", "title", "-p", "prog", "-parent", "ep-abc123"}, want: 2},
		{name: "stops at double dash", args: []string{"add", "-p", "prog", "--", "-p", "1"}, want: 1},
		{name: "priority long form ignored", args: []string{"add", "title", "-p", "prog", "--priority", "1"}, want: 1},
		{name: "parent long form ignored", args: []string{"add", "title", "-p", "prog", "--parent", "ep-abc123"}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countProjectFlags(tt.args); got != tt.want {
				t.Fatalf("countProjectFlags(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestRejectDuplicateProjectFlags(t *testing.T) {
	if err := rejectDuplicateProjectFlags([]string{"add", "-p", "prog"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := rejectDuplicateProjectFlags([]string{"add", "-p", "prog", "-p", "1"})
	if err == nil {
		t.Fatal("expected error for duplicate -p")
	}
	if !strings.Contains(err.Error(), "--priority") || !strings.Contains(err.Error(), "--parent") {
		t.Fatalf("error should mention --priority/--parent, got: %v", err)
	}
}

func TestValidateProjectName(t *testing.T) {
	tests := []struct {
		name    string
		project string
		wantErr string
	}{
		{name: "normal", project: "prog"},
		{name: "priority 1", project: "1", wantErr: "--priority 1"},
		{name: "priority 2", project: "2", wantErr: "--priority 2"},
		{name: "priority 3", project: "3", wantErr: "--priority 3"},
		{name: "priority 4 ok", project: "4"},
		{name: "task id", project: "ts-a1b2c3", wantErr: "--parent ts-a1b2c3"},
		{name: "epic id", project: "ep-abcdef", wantErr: "--parent ep-abcdef"},
		{name: "uppercase hex rejected as id", project: "ep-ABCDEF"},
		{name: "too short id", project: "ep-abc12"},
		{name: "almost id prefix", project: "epic-abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectName(tt.project)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err.Error(), tt.wantErr)
			}
			if !strings.Contains(err.Error(), "-p is --project") {
				t.Fatalf("error should clarify -p is --project, got: %v", err)
			}
		})
	}
}

func TestLooksLikeItemID(t *testing.T) {
	if !looksLikeItemID("ts-a1b2c3") {
		t.Error("expected ts-a1b2c3 to match")
	}
	if !looksLikeItemID("ep-000000") {
		t.Error("expected ep-000000 to match")
	}
	if looksLikeItemID("ts-A1B2C3") {
		t.Error("uppercase hex should not match")
	}
	if looksLikeItemID("xx-a1b2c3") {
		t.Error("unknown prefix should not match")
	}
}
