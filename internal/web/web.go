// Package web serves a read-only dependency graph and embedded browser assets.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/baiirun/prog/internal/db"
	"github.com/baiirun/prog/internal/model"
)

//go:embed static
var staticFS embed.FS

// GraphNode is one task or epic in the /api/graph response.
type GraphNode struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	Project     string   `json:"project"`
	Labels      []string `json:"labels"`
	Blocked     bool     `json:"blocked"`
	ParentID    *string  `json:"parent_id,omitempty"`
}

// GraphEdge is one blocks relationship: From is blocked by To.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GraphResponse is the /api/graph payload.
type GraphResponse struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// OpenDB opens the prog database (including migrations). It mirrors
// openDB in cmd/prog so the server sees the same data as the CLI.
func OpenDB() (*db.DB, error) {
	path, err := db.DefaultPath()
	if err != nil {
		return nil, err
	}
	database, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// HandlerForDB does not own the database; its caller must close it.
func HandlerForDB(database *db.DB) http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/graph", func(w http.ResponseWriter, r *http.Request) {
		handleGraph(database, w, r)
	})
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	return mux
}

// Serve starts the graph server on host:port (0 = pick one port) and
// opens the browser unless noOpen. It blocks until the server stops.
func Serve(host string, port int, noOpen bool) error {
	database, err := OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Printf("prog web: %s\n", url)
	if !noOpen {
		openBrowser(url)
	}
	server := &http.Server{Handler: HandlerForDB(database), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	return server.Serve(ln)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// handleGraph serves GET /api/graph?project=&status=&label=&q=&hide_done=1.
//
// Nodes come from ListItemsFiltered (derived epic status included).
// Edges come from GetAllDeps, restricted to the visible node set.
// Blocked uses the database's resolution rules, regardless of visibility.
func handleGraph(database *db.DB, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := q.Get("project")
	statusStr := q.Get("status")
	labels := q["label"]
	search := strings.ToLower(q.Get("q"))
	hideDone := q.Get("hide_done") == "1" || q.Get("hide_done") == "true"

	filter := db.ListFilter{Project: project, Labels: labels}
	if statusStr != "" {
		s := model.Status(statusStr)
		if !s.IsValid() {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
		filter.Status = &s
	}

	items, err := database.ListItemsFiltered(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := database.PopulateItemLabels(items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodes := make([]GraphNode, 0, len(items))
	visible := make(map[string]bool, len(items))
	for _, item := range items {
		if hideDone && (item.Status == model.StatusDone || item.Status == model.StatusCanceled) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(item.Title), search) &&
			!strings.Contains(strings.ToLower(item.ID), search) &&
			!strings.Contains(strings.ToLower(item.Description), search) {
			continue
		}
		visible[item.ID] = true
		labels := item.Labels
		if labels == nil {
			labels = []string{}
		}
		nodes = append(nodes, GraphNode{
			ID:          item.ID,
			Title:       item.Title,
			Description: item.Description,
			Status:      string(item.Status),
			Type:        string(item.Type),
			Project:     item.Project,
			Labels:      labels,
			ParentID:    item.ParentID,
		})
	}

	allEdges, err := database.GetAllDeps("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	edges := make([]GraphEdge, 0, len(allEdges))
	// Do not derive readiness from displayed epic status. The CLI uses the
	// database's dependency-resolution expression, including terminal epics.
	blockedItems, err := database.ListItemsFiltered(db.ListFilter{HasBlockers: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, e := range allEdges {
		if !visible[e.ItemID] || !visible[e.DependsOnID] {
			continue
		}
		edges = append(edges, GraphEdge{From: e.ItemID, To: e.DependsOnID})
	}
	blockedBy := make(map[string]bool, len(nodes))
	for _, item := range blockedItems {
		blockedBy[item.ID] = true
	}
	for i := range nodes {
		nodes[i].Blocked = blockedBy[nodes[i].ID]
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(GraphResponse{Nodes: nodes, Edges: edges})
}
