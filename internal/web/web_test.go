package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/db"
	"github.com/baiirun/prog/internal/model"
)

func fixtureHandler(t *testing.T) http.Handler {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now()
	items := []*model.Item{
		{ID: "ep-root1", Project: "fixture", Type: model.ItemTypeEpic, Title: "Root", Status: model.StatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "ep-nest1", Project: "fixture", Type: model.ItemTypeEpic, Title: "Nested", Status: model.StatusDraft, ParentID: stringPtr("ep-root1"), CreatedAt: now, UpdatedAt: now},
		{ID: "ts-block1", Project: "fixture", Type: model.ItemTypeTask, Title: "Blocker", Status: model.StatusOpen, CreatedAt: now, UpdatedAt: now},
		{ID: "ts-child1", Project: "fixture", Type: model.ItemTypeTask, Title: "Child", Status: model.StatusOpen, ParentID: stringPtr("ep-nest1"), CreatedAt: now, UpdatedAt: now},
	}
	for _, item := range items {
		if err := database.CreateItem(item); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.AddDep("ts-child1", "ts-block1"); err != nil {
		t.Fatal(err)
	}
	return HandlerForDB(database)
}

func stringPtr(value string) *string { return &value }

func graph(t *testing.T, handler http.Handler, path string) GraphResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response GraphResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestGraphFixtureIncludesParentAndActualEdges(t *testing.T) {
	response := graph(t, fixtureHandler(t), "/api/graph?project=fixture")
	var child GraphNode
	for _, node := range response.Nodes {
		if node.ID == "ts-child1" {
			child = node
		}
	}
	if child.ParentID == nil || *child.ParentID != "ep-nest1" {
		t.Fatalf("parent_id = %v, want ep-nest1", child.ParentID)
	}
	if len(response.Edges) != 1 || response.Edges[0].From != "ts-child1" || response.Edges[0].To != "ts-block1" {
		t.Fatalf("edges = %#v", response.Edges)
	}
}

func TestGraphBlockedTruthSurvivesVisibilityFilter(t *testing.T) {
	response := graph(t, fixtureHandler(t), "/api/graph?project=fixture&q=child")
	if len(response.Nodes) != 1 || response.Nodes[0].ID != "ts-child1" || !response.Nodes[0].Blocked {
		t.Fatalf("child must remain blocked when its blocker is hidden: %#v", response.Nodes)
	}
	if len(response.Edges) != 0 {
		t.Fatal("filtered endpoint should not produce a dangling edge")
	}
}

func TestGraphEmptyAndInvalidRequests(t *testing.T) {
	handler := fixtureHandler(t)
	response := graph(t, handler, "/api/graph?project=absent")
	if response.Nodes == nil || response.Edges == nil || len(response.Nodes) != 0 || len(response.Edges) != 0 {
		t.Fatalf("expected empty arrays, got %#v", response)
	}
	for _, request := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/graph?status=invalid", 400},
		{"POST", "/api/graph", 405},
		{"GET", "/", 200},
		{"GET", "/projection.js", 200},
		{"GET", "/vendor/LineSegments2.js", 200},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
		if recorder.Code != request.status {
			t.Fatalf("%s %s = %d, want %d", request.method, request.path, recorder.Code, request.status)
		}
	}
}
