package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

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

func TestContextTaskRankingShowsMatchReasons(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()

	task := &model.Item{
		ID: "ts-ctx001", Project: "test", Type: model.ItemTypeTask,
		Title: "Fix authn handshake", Description: "Investigate concurrency around batching",
		Status: model.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateItem(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	start, end := 10, 20
	if err := database.CreateLearning(&model.Learning{
		ID: "lrn-ctx001", CreatedAt: now, UpdatedAt: now,
		Summary:  "Remember the handshake order",
		Status:   model.LearningStatusActive,
		Concepts: []string{"authn"},
		Sources: []model.LearningSource{
			{Path: "internal/auth/handshake.go", StartLine: &start, EndLine: &end, Note: "order"},
		},
	}); err != nil {
		t.Fatalf("create learning: %v", err)
	}
	if err := database.CreateLearning(&model.Learning{
		ID: "lrn-ctx000", CreatedAt: now, UpdatedAt: now,
		Summary:  "Unrelated CSS layout",
		Status:   model.LearningStatusActive,
		Concepts: []string{"ui"},
	}); err != nil {
		t.Fatalf("create unrelated learning: %v", err)
	}

	hits, err := database.SearchKnowledge(model.KnowledgeQuery{TaskID: task.ID})
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected ranked hits for task")
	}
	if hits[0].Learning.ID != "lrn-ctx001" {
		t.Fatalf("top hit = %s, want lrn-ctx001", hits[0].Learning.ID)
	}
	if len(hits[0].Reasons) == 0 {
		t.Fatal("expected match reasons on ranked hit")
	}

	output := captureOutput(func() {
		printKnowledgeHits(hits)
	})
	if !strings.Contains(output, "lrn-ctx001") {
		t.Fatalf("missing learning id, output:\n%s", output)
	}
	if !strings.Contains(output, "Matched:") {
		t.Fatalf("missing match reasons, output:\n%s", output)
	}
	if !strings.Contains(output, model.MatchTaskTitle) && !strings.Contains(output, model.MatchConceptName) {
		t.Fatalf("expected task/concept reason signals, output:\n%s", output)
	}
	if !strings.Contains(output, "Evidence: internal/auth/handshake.go:10-20 (order)") {
		t.Fatalf("expected typed evidence with line range, output:\n%s", output)
	}
	if strings.Contains(output, "lrn-ctx000") {
		t.Fatalf("unrelated learning should not appear, output:\n%s", output)
	}
}

func TestContextKnowledgeJSONShape(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	line := 42

	if err := database.CreateLearning(&model.Learning{
		ID: "lrn-json01", CreatedAt: now, UpdatedAt: now,
		Summary:  "Token refresh must be idempotent",
		Detail:   "Retry with the same nonce",
		Status:   model.LearningStatusActive,
		Concepts: []string{"auth"},
		Sources:  []model.LearningSource{{Path: "auth.go", StartLine: &line}},
	}); err != nil {
		t.Fatalf("create learning: %v", err)
	}

	hits, err := database.SearchKnowledge(model.KnowledgeQuery{Text: "Token refresh must be idempotent"})
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}

	output := captureOutput(func() {
		if err := printKnowledgeHitsJSON(hits); err != nil {
			t.Fatalf("printKnowledgeHitsJSON: %v", err)
		}
	})

	var result []LearningJSON
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, output)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	got := result[0]
	if got.ID != "lrn-json01" {
		t.Errorf("id = %q", got.ID)
	}
	if len(got.Reasons) == 0 {
		t.Fatal("expected reasons in JSON")
	}
	if got.Reasons[0].Signal == "" {
		t.Fatal("reason signal empty")
	}
	if strings.Contains(output, `"score"`) {
		t.Fatal("JSON must not expose an unexplained score")
	}
	if len(got.Sources) != 1 || got.Sources[0].Path != "auth.go" {
		t.Fatalf("sources = %+v", got.Sources)
	}
	if got.Sources[0].StartLine == nil || *got.Sources[0].StartLine != 42 {
		t.Fatalf("start_line = %v, want 42", got.Sources[0].StartLine)
	}
}

func TestFormatLearningSourceLineRanges(t *testing.T) {
	start, end := 3, 9
	cases := []struct {
		src  model.LearningSource
		want string
	}{
		{model.LearningSource{Path: "a.go"}, "a.go"},
		{model.LearningSource{Path: "a.go", StartLine: &start}, "a.go:3"},
		{model.LearningSource{Path: "a.go", StartLine: &start, EndLine: &end}, "a.go:3-9"},
		{model.LearningSource{Path: "a.go", StartLine: &start, EndLine: &end, Note: "n"}, "a.go:3-9 (n)"},
	}
	for _, tc := range cases {
		if got := formatLearningSource(tc.src); got != tc.want {
			t.Errorf("formatLearningSource(%+v) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestLearnSupersedeSuccess(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()

	old := &model.Learning{
		ID: "lrn-old002", CreatedAt: now, UpdatedAt: now,
		Summary: "Old advice", Status: model.LearningStatusActive, Concepts: []string{"cli"},
	}
	neu := &model.Learning{
		ID: "lrn-new002", CreatedAt: now, UpdatedAt: now,
		Summary: "New advice", Status: model.LearningStatusActive, Concepts: []string{"cli"},
	}
	if err := database.CreateLearning(old); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := database.CreateLearning(neu); err != nil {
		t.Fatalf("create new: %v", err)
	}

	output := captureOutput(func() {
		if err := database.SupersedeLearning(neu.ID, old.ID); err != nil {
			t.Fatalf("SupersedeLearning: %v", err)
		}
		fmt.Printf("Superseded %s with %s\n", old.ID, neu.ID)
	})
	want := "Superseded lrn-old002 with lrn-new002\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}

	gotOld, err := database.GetLearning(old.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if gotOld.Status != model.LearningStatusStale {
		t.Fatalf("old status = %s, want stale", gotOld.Status)
	}

	gotNew, err := database.GetLearning(neu.ID)
	if err != nil {
		t.Fatalf("get new: %v", err)
	}
	found := false
	for _, r := range gotNew.Relations {
		if r.Kind == model.LearningRelationKindSupersedes && r.SourceID == neu.ID && r.TargetID == old.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing supersedes relation on replacement: %+v", gotNew.Relations)
	}

	printOut := captureOutput(func() {
		printLearnings([]model.Learning{*gotOld, *gotNew})
	})
	if !strings.Contains(printOut, "Superseded by: lrn-new002") {
		t.Fatalf("expected superseded-by rendering, output:\n%s", printOut)
	}
	if !strings.Contains(printOut, "Supersedes: lrn-old002") {
		t.Fatalf("expected supersedes rendering, output:\n%s", printOut)
	}
}

func TestLearnSupersedeInvalidErrors(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	active := &model.Learning{
		ID: "lrn-inv001", CreatedAt: now, UpdatedAt: now,
		Summary: "Active", Status: model.LearningStatusActive, Concepts: []string{"cli"},
	}
	if err := database.CreateLearning(active); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := database.SupersedeLearning(active.ID, active.ID)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("self-link error = %v", err)
	}

	err = database.SupersedeLearning("lrn-missing", active.ID)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing replacement error = %v", err)
	}

	err = database.SupersedeLearning(active.ID, "lrn-missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing replaced error = %v", err)
	}
}
