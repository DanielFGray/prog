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

func seedTokenRefreshHit(t *testing.T) []model.KnowledgeHit {
	t.Helper()
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
	return hits
}

func captureContextJSON(t *testing.T, hits []model.KnowledgeHit, summaryOnly bool) ContextResultJSON {
	t.Helper()
	output := captureOutput(func() {
		page := contextPage{Total: len(hits), Truncated: false, Limit: defaultContextLimit}
		if err := printContextLearningsJSON(knowledgeHitsToLearningJSON(hits), summaryOnly, page); err != nil {
			t.Fatalf("printContextLearningsJSON: %v", err)
		}
	})
	var result ContextResultJSON
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, output)
	}
	if strings.Contains(output, `"score"`) {
		t.Fatal("JSON must not expose an unexplained score")
	}
	return result
}

func TestContextKnowledgeJSONShape_Envelope(t *testing.T) {
	result := captureContextJSON(t, seedTokenRefreshHit(t), true)
	if result.Total != 1 || result.Returned != 1 || result.Truncated || result.Limit != defaultContextLimit {
		t.Fatalf("envelope = %+v", result)
	}
	if len(result.Learnings) != 1 {
		t.Fatalf("len = %d, want 1", len(result.Learnings))
	}
}

func TestContextKnowledgeJSONShape_SummaryOmitsDetail(t *testing.T) {
	result := captureContextJSON(t, seedTokenRefreshHit(t), true)
	if result.Learnings[0].Detail != "" {
		t.Fatalf("summary JSON must omit detail, got %q", result.Learnings[0].Detail)
	}
}

func TestContextKnowledgeJSONShape_ReasonsAndSources(t *testing.T) {
	result := captureContextJSON(t, seedTokenRefreshHit(t), true)
	got := result.Learnings[0]
	if got.ID != "lrn-json01" {
		t.Errorf("id = %q", got.ID)
	}
	if len(got.Reasons) == 0 || got.Reasons[0].Signal == "" {
		t.Fatalf("expected non-empty reason signal, got %+v", got.Reasons)
	}
	if len(got.Sources) != 1 || got.Sources[0].Path != "auth.go" {
		t.Fatalf("sources = %+v", got.Sources)
	}
	if got.Sources[0].StartLine == nil || *got.Sources[0].StartLine != 42 {
		t.Fatalf("start_line = %v, want 42", got.Sources[0].StartLine)
	}
}

func TestContextDefaultSummaryAndFull(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()

	if err := database.CreateLearning(&model.Learning{
		ID: "lrn-sum001", CreatedAt: now, UpdatedAt: now,
		Summary:  "Summary-only default path",
		Detail:   "SECRET_DETAIL_BODY",
		Status:   model.LearningStatusActive,
		Concepts: []string{"auth"},
	}); err != nil {
		t.Fatalf("create learning: %v", err)
	}

	summaryOut := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Concepts: []string{"auth"},
			Limit:    defaultContextLimit,
		}); err != nil {
			t.Fatalf("runContext summary: %v", err)
		}
	})
	if !strings.Contains(summaryOut, "lrn-sum001") {
		t.Fatalf("missing id in summary output:\n%s", summaryOut)
	}
	if !strings.Contains(summaryOut, "Summary-only default path") {
		t.Fatalf("missing summary text:\n%s", summaryOut)
	}
	if strings.Contains(summaryOut, "SECRET_DETAIL_BODY") {
		t.Fatalf("default output must not include detail:\n%s", summaryOut)
	}
	if !strings.Contains(summaryOut, "concept_name") {
		t.Fatalf("expected match reasons in summary output:\n%s", summaryOut)
	}

	fullOut := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Concepts: []string{"auth"},
			Full:     true,
			Limit:    defaultContextLimit,
		}); err != nil {
			t.Fatalf("runContext full: %v", err)
		}
	})
	if !strings.Contains(fullOut, "SECRET_DETAIL_BODY") {
		t.Fatalf("--full must include detail:\n%s", fullOut)
	}
	if !strings.Contains(fullOut, "Matched:") {
		t.Fatalf("--full must include match reasons:\n%s", fullOut)
	}
}

func TestContextDefaultLimitAndExplicitLimit(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()

	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("lrn-lim%03d", i)
		if err := database.CreateLearning(&model.Learning{
			ID: id, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
			Summary:  fmt.Sprintf("Limit learning %d uniquephrase", i),
			Detail:   "body",
			Status:   model.LearningStatusActive,
			Concepts: []string{"limits"},
		}); err != nil {
			t.Fatalf("create learning %d: %v", i, err)
		}
	}

	defaultOut := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Query: "uniquephrase",
			Limit: defaultContextLimit,
		}); err != nil {
			t.Fatalf("runContext default limit: %v", err)
		}
	})
	if !strings.Contains(defaultOut, "(showing 10 of 12; pass --limit N to see more)") {
		t.Fatalf("expected truncation footer:\n%s", defaultOut)
	}
	shown := strings.Count(defaultOut, "lrn-lim")
	if shown != 10 {
		t.Fatalf("default shown ids = %d, want 10\n%s", shown, defaultOut)
	}

	limitedOut := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Query: "uniquephrase",
			Limit: 3,
		}); err != nil {
			t.Fatalf("runContext limit 3: %v", err)
		}
	})
	if !strings.Contains(limitedOut, "(showing 3 of 12; pass --limit N to see more)") {
		t.Fatalf("expected limit-3 footer:\n%s", limitedOut)
	}
	if strings.Count(limitedOut, "lrn-lim") != 3 {
		t.Fatalf("limit-3 shown = %d, want 3\n%s", strings.Count(limitedOut, "lrn-lim"), limitedOut)
	}
}

func TestContextIDBypassesLimit(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	if err := database.CreateLearning(&model.Learning{
		ID: "lrn-idbypass", CreatedAt: now, UpdatedAt: now,
		Summary: "ID resolve",
		Detail:  "FULL_ID_BODY",
		Status:  model.LearningStatusActive,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	out := captureOutput(func() {
		if err := runContext(database, contextOptions{
			ID:    "lrn-idbypass",
			Limit: 0, // would suppress ranked results; --id bypasses
		}); err != nil {
			t.Fatalf("runContext id: %v", err)
		}
	})
	if !strings.Contains(out, "FULL_ID_BODY") {
		t.Fatalf("--id must return full body:\n%s", out)
	}
	if strings.Contains(out, "showing") {
		t.Fatalf("--id must not report ranked truncation:\n%s", out)
	}
}

func TestContextBareUnscopedRejected(t *testing.T) {
	database := setupTestDB(t)
	err := runContext(database, contextOptions{Limit: defaultContextLimit})
	if err == nil {
		t.Fatal("expected error for bare unscoped context")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Fatalf("error = %v, want mention of --all", err)
	}

	// --summary alone is a compatibility no-op; still requires a scope or --all.
	err = runContext(database, contextOptions{Limit: defaultContextLimit})
	if err == nil {
		t.Fatal("expected rejection when no ranked filters and no --all")
	}
}

func TestContextAllHonorsDefaultCap(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	for i := 0; i < 12; i++ {
		if err := database.CreateLearning(&model.Learning{
			ID: fmt.Sprintf("lrn-all%03d", i), CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
			Summary:  fmt.Sprintf("All corpus %d", i),
			Status:   model.LearningStatusActive,
			Concepts: []string{"corpus"},
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	out := captureOutput(func() {
		if err := runContext(database, contextOptions{
			All:   true,
			Limit: defaultContextLimit,
		}); err != nil {
			t.Fatalf("runContext --all: %v", err)
		}
	})
	if !strings.Contains(out, "(showing 10 of 12; pass --limit N to see more)") {
		t.Fatalf("expected --all truncation:\n%s", out)
	}
	if strings.Count(out, "lrn-all") != 10 {
		t.Fatalf("--all shown = %d, want 10\n%s", strings.Count(out, "lrn-all"), out)
	}

	raised := captureOutput(func() {
		if err := runContext(database, contextOptions{
			All:   true,
			Limit: 12,
		}); err != nil {
			t.Fatalf("runContext --all --limit 12: %v", err)
		}
	})
	if strings.Contains(raised, "showing") {
		t.Fatalf("explicit limit covering all must not truncate:\n%s", raised)
	}
	if strings.Count(raised, "lrn-all") != 12 {
		t.Fatalf("raised shown = %d, want 12\n%s", strings.Count(raised, "lrn-all"), raised)
	}
}

func TestContextJSONParityWithTextSelection(t *testing.T) {
	database := setupTestDB(t)
	now := time.Now()
	for i := 0; i < 12; i++ {
		if err := database.CreateLearning(&model.Learning{
			ID: fmt.Sprintf("lrn-jpar%03d", i), CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
			Summary:  fmt.Sprintf("JSON parity %d sharedtoken", i),
			Detail:   fmt.Sprintf("detail-%d", i),
			Status:   model.LearningStatusActive,
			Concepts: []string{"parity"},
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	jsonOut := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Query: "sharedtoken",
			Limit: defaultContextLimit,
			JSON:  true,
		}); err != nil {
			t.Fatalf("runContext json: %v", err)
		}
	})
	var result ContextResultJSON
	if err := json.Unmarshal([]byte(jsonOut), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
	}
	if result.Total != 12 || result.Returned != 10 || !result.Truncated || result.Limit != 10 {
		t.Fatalf("envelope = %+v", result)
	}
	if len(result.Learnings) != 10 {
		t.Fatalf("learnings = %d, want 10", len(result.Learnings))
	}
	for _, l := range result.Learnings {
		if l.Detail != "" {
			t.Fatalf("default JSON must omit detail, got %q on %s", l.Detail, l.ID)
		}
		if len(l.Reasons) == 0 {
			t.Fatalf("expected reasons on %s", l.ID)
		}
	}

	fullJSON := captureOutput(func() {
		if err := runContext(database, contextOptions{
			Query: "sharedtoken",
			Limit: 2,
			Full:  true,
			JSON:  true,
		}); err != nil {
			t.Fatalf("runContext full json: %v", err)
		}
	})
	var full ContextResultJSON
	if err := json.Unmarshal([]byte(fullJSON), &full); err != nil {
		t.Fatalf("invalid full JSON: %v\n%s", err, fullJSON)
	}
	if full.Total != 12 || full.Returned != 2 || !full.Truncated {
		t.Fatalf("full envelope = %+v", full)
	}
	if full.Learnings[0].Detail == "" {
		t.Fatal("--full JSON must include detail")
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
