package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/baiirun/prog/internal/model"
)

// depUnresolvedExpr is a SQL expression (referencing alias "i" for the dependency item)
// that is true when the dependency is NOT resolved. A dependency is resolved when:
//   - Its stored status is done or canceled, OR
//   - It's an epic with children and ALL children are done or canceled.
//
// This handles derived epic status at the SQL level so that tasks depending on
// an epic become unblocked when all the epic's children complete.
const depUnresolvedExpr = `NOT (
			-- Direct resolution: stored status is terminal
			i.status IN ('done', 'canceled')
			OR (
				-- Derived resolution: epic with children, all children terminal
				i.type = 'epic'
				AND EXISTS (SELECT 1 FROM items c WHERE c.parent_id = i.id)           -- has at least one child
				AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id = i.id
				                AND c.status NOT IN ('done', 'canceled'))              -- no non-terminal children
			)
		)`

// labelFilterClause returns a SQL clause and args that filter items to those
// having ALL specified labels (AND semantics). Returns empty string and nil args
// if labels is empty.
func labelFilterClause(labels []string) (string, []any) {
	if len(labels) == 0 {
		return "", nil
	}
	placeholders := ""
	for i := range labels {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
	}
	clause := fmt.Sprintf(` AND id IN (
		SELECT il.item_id FROM item_labels il
		JOIN labels l ON il.label_id = l.id
		WHERE l.name IN (%s)
		GROUP BY il.item_id
		HAVING COUNT(DISTINCT l.name) = ?
	)`, placeholders)
	args := make([]any, 0, len(labels)+1)
	for _, label := range labels {
		args = append(args, label)
	}
	args = append(args, len(labels))
	return clause, args
}

// ListFilter contains optional filters for listing items.
type ListFilter struct {
	Project     string        // Filter by project
	Status      *model.Status // Filter by status
	Parent      string        // Filter by parent epic ID
	Type        string        // Filter by item type (task, epic)
	Blocking    string        // Show items that block this ID
	BlockedBy   string        // Show items blocked by this ID
	HasBlockers bool          // Show only items with unresolved blockers
	NoBlockers  bool          // Show only items with no blockers
	Labels      []string      // Filter by label names (AND - items must have all)
}

// itemColumns is the projection every item read shares, so scanning stays in
// one place (queryItems). The trailing subquery carries the item's newest log
// timestamp; queryItems folds it together with updated_at into LastActivityAt.
// The fold happens in Go rather than SQL because the two columns are written in
// different formats and zones — items.updated_at is a Go local timestamp with a
// monotonic suffix, logs.created_at is SQLite's UTC CURRENT_TIMESTAMP — so a SQL
// MAX() over them would compare the strings lexically and be wrong by hours.
const itemColumns = `id, project, type, title, description, definition_of_done, status, priority, parent_id, created_at, updated_at,
		(SELECT MAX(l.created_at) FROM logs l WHERE l.item_id = items.id) AS last_log_at`

// ListItems returns items filtered by project and/or status.
func (db *DB) ListItems(project string, status *model.Status) ([]model.Item, error) {
	return db.ListItemsFiltered(ListFilter{Project: project, Status: status})
}

// ListItemsFiltered returns items matching the given filters.
func (db *DB) ListItemsFiltered(filter ListFilter) ([]model.Item, error) {
	query := `SELECT ` + itemColumns + ` FROM items WHERE 1=1`
	args := []any{}

	if filter.Project != "" {
		query += ` AND project = ?`
		args = append(args, filter.Project)
	}
	// When filtering by status, include all epics in the SQL results since their
	// effective status is derived from children (applied in queryItems). Epics whose
	// derived status doesn't match will be filtered out in the post-query step below.
	statusFilter := filter.Status
	if filter.Status != nil {
		if !filter.Status.IsValid() {
			return nil, fmt.Errorf("invalid status: %s", *filter.Status)
		}
		query += ` AND (status = ? OR type = 'epic')`
		args = append(args, *filter.Status)
	}
	if filter.Parent != "" {
		query += ` AND parent_id = ?`
		args = append(args, filter.Parent)
	}
	if filter.Type != "" {
		itemType := model.ItemType(filter.Type)
		if !itemType.IsValid() {
			return nil, fmt.Errorf("invalid type: %s (valid: task, epic)", filter.Type)
		}
		query += ` AND type = ?`
		args = append(args, filter.Type)
	}
	if filter.Blocking != "" {
		// Items that block the given ID (i.e., items the given ID depends on)
		query += ` AND id IN (SELECT depends_on FROM deps WHERE item_id = ?)`
		args = append(args, filter.Blocking)
	}
	if filter.BlockedBy != "" {
		// Items blocked by the given ID (i.e., items that depend on the given ID)
		query += ` AND id IN (SELECT item_id FROM deps WHERE depends_on = ?)`
		args = append(args, filter.BlockedBy)
	}
	if filter.HasBlockers {
		// Items with unresolved blockers (dependencies that aren't done)
		query += ` AND id IN (SELECT d.item_id FROM deps d JOIN items i ON d.depends_on = i.id WHERE ` + depUnresolvedExpr + `)`
	}
	if filter.NoBlockers {
		// Items with no blockers (either no deps, or all deps are done)
		query += ` AND id NOT IN (SELECT d.item_id FROM deps d JOIN items i ON d.depends_on = i.id WHERE ` + depUnresolvedExpr + `)`
	}
	if clause, labelArgs := labelFilterClause(filter.Labels); clause != "" {
		query += clause
		args = append(args, labelArgs...)
	}
	query += ` ORDER BY priority ASC, created_at ASC`

	items, err := db.queryItems(query, args...)
	if err != nil {
		return nil, err
	}

	// Post-filter: when a status filter was set, we included all epics in the SQL
	// to capture those whose derived status matches. Remove non-matching epics.
	if statusFilter != nil {
		filtered := items[:0]
		for _, item := range items {
			if item.Status == *statusFilter {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	return items, nil
}

// ReadyItems returns items that are open and have no unmet dependencies.
func (db *DB) ReadyItems(project string) ([]model.Item, error) {
	return db.ReadyItemsFiltered(project, nil)
}

// ReadyItemsFiltered returns ready items with optional label filtering.
func (db *DB) ReadyItemsFiltered(project string, labels []string) ([]model.Item, error) {
	query := `
		SELECT ` + itemColumns + `
		FROM items
		WHERE status = 'open'
		  AND type = 'task'
		  AND id NOT IN (
		    SELECT d.item_id FROM deps d
		    JOIN items i ON d.depends_on = i.id
		    WHERE ` + depUnresolvedExpr + `
		  )`
	args := []any{}

	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	if clause, labelArgs := labelFilterClause(labels); clause != "" {
		query += clause
		args = append(args, labelArgs...)
	}
	query += ` ORDER BY priority ASC, created_at ASC`

	return db.queryItems(query, args...)
}

// StatusReport contains aggregated project status.
type StatusReport struct {
	Project        string
	Draft          int
	Open           int
	InProgress     int
	Blocked        int
	Reviewing      int
	Done           int
	Canceled       int
	Ready          int
	DraftItems     []model.Item // being defined/scoped
	RecentDone     []model.Item // last 3 completed
	InProgItems    []model.Item // current in-progress
	ReviewingItems []model.Item // awaiting merge
	BlockedItems   []model.Item // blocked with reasons
	ReadyItems     []model.Item // ready for work
}

// ProjectStatus returns an aggregated status report for a project.
func (db *DB) ProjectStatus(project string) (*StatusReport, error) {
	return db.ProjectStatusFiltered(project, nil)
}

// ProjectStatusFiltered returns an aggregated status report with optional label filtering.
// Epic statuses are derived from child task state rather than using stored status.
func (db *DB) ProjectStatusFiltered(project string, labels []string) (*StatusReport, error) {
	report := &StatusReport{Project: project}

	// Fetch all items (with derived epic status applied by queryItems)
	allItems, err := db.ListItemsFiltered(ListFilter{Project: project, Labels: labels})
	if err != nil {
		return nil, err
	}

	// Count by derived status and categorize items
	for _, item := range allItems {
		switch item.Status {
		case model.StatusDraft:
			report.Draft++
			report.DraftItems = append(report.DraftItems, item)
		case model.StatusOpen:
			report.Open++
		case model.StatusInProgress:
			report.InProgress++
			report.InProgItems = append(report.InProgItems, item)
		case model.StatusBlocked:
			report.Blocked++
			report.BlockedItems = append(report.BlockedItems, item)
		case model.StatusReviewing:
			report.Reviewing++
			report.ReviewingItems = append(report.ReviewingItems, item)
		case model.StatusDone:
			report.Done++
		case model.StatusCanceled:
			report.Canceled++
		default:
			return nil, fmt.Errorf("ProjectStatusFiltered: unknown status %q for item %s", item.Status, item.ID)
		}
	}

	// Get ready count and items
	readyItems, err := db.ReadyItemsFiltered(project, labels)
	if err != nil {
		return nil, err
	}
	report.Ready = len(readyItems)
	report.ReadyItems = readyItems

	// Get recent done (last 3, sorted by updated_at desc)
	// We need to query specifically because we need ordering by updated_at
	recentQuery := `
		SELECT ` + itemColumns + `
		FROM items WHERE status IN ('done', 'canceled')`
	recentArgs := []any{}
	if project != "" {
		recentQuery += ` AND project = ?`
		recentArgs = append(recentArgs, project)
	}
	if clause, labelArgs := labelFilterClause(labels); clause != "" {
		recentQuery += clause
		recentArgs = append(recentArgs, labelArgs...)
	}
	// LIMIT 10 (not 3) because derived-done epics stored as 'open' aren't in
	// the SQL WHERE; we overfetch, then post-filter to the first 3 derived-done.
	recentQuery += ` ORDER BY updated_at DESC LIMIT 10`
	recentCandidates, err := db.queryItems(recentQuery, recentArgs...)
	if err != nil {
		return nil, err
	}
	// Filter to items whose derived status is done (queryItems already applied derivation)
	for _, item := range recentCandidates {
		if item.Status == model.StatusDone && len(report.RecentDone) < 3 {
			report.RecentDone = append(report.RecentDone, item)
		}
	}

	return report, nil
}

// ListProjects returns all project names from the projects table.
func (db *DB) ListProjects() ([]string, error) {
	rows, err := db.Query(`SELECT name FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var projects []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		projects = append(projects, name)
	}
	return projects, rows.Err()
}

// RenameProject renames a project. If the target already exists, it merges
// all items and labels into the target and deletes the source project.
// Learnings and concepts are not project-scoped, so a rename does not move
// them.
func (db *DB) RenameProject(oldName, newName string) error {
	if oldName == newName {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Check if old project exists
	var exists bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name = ?)`, oldName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check project: %w", err)
	}
	if !exists {
		return fmt.Errorf("project not found: %s", oldName)
	}

	// Check if target exists (merge vs rename)
	var targetExists bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name = ?)`, newName).Scan(&targetExists)
	if err != nil {
		return fmt.Errorf("failed to check target project: %w", err)
	}

	now := time.Now()

	// Update items
	_, err = tx.Exec(`UPDATE items SET project = ?, updated_at = ? WHERE project = ?`, newName, now, oldName)
	if err != nil {
		return fmt.Errorf("failed to update items: %w", err)
	}

	// Update labels (skip if label name already exists in target)
	_, err = tx.Exec(`
		UPDATE labels SET project = ?, updated_at = ?
		WHERE project = ?
		AND name NOT IN (SELECT name FROM labels WHERE project = ?)
	`, newName, now, oldName, newName)
	if err != nil {
		return fmt.Errorf("failed to update labels: %w", err)
	}
	// Before deleting duplicate labels, repoint item_labels to target's label
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO item_labels (item_id, label_id)
		SELECT il.item_id, dst.id
		FROM item_labels il
		JOIN labels src ON il.label_id = src.id AND src.project = ?
		JOIN labels dst ON dst.name = src.name AND dst.project = ?
	`, oldName, newName)
	if err != nil {
		return fmt.Errorf("failed to reassociate item labels: %w", err)
	}
	// Delete item_labels referencing the old duplicate labels
	_, err = tx.Exec(`
		DELETE FROM item_labels WHERE label_id IN (
			SELECT id FROM labels WHERE project = ?
		)
	`, oldName)
	if err != nil {
		return fmt.Errorf("failed to delete old item label refs: %w", err)
	}
	_, err = tx.Exec(`DELETE FROM labels WHERE project = ?`, oldName)
	if err != nil {
		return fmt.Errorf("failed to delete duplicate labels: %w", err)
	}

	// Note: deps table has no project column - it references items directly.
	// Learnings and concepts are global and so are untouched by a rename.

	// Delete old project
	_, err = tx.Exec(`DELETE FROM projects WHERE name = ?`, oldName)
	if err != nil {
		return fmt.Errorf("failed to delete old project: %w", err)
	}

	// Ensure target project exists
	if !targetExists {
		_, err = tx.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES (?, ?, ?)`, newName, now, now)
		if err != nil {
			return fmt.Errorf("failed to create target project: %w", err)
		}
	}

	return tx.Commit()
}

// queryItems is a helper to scan item rows.
// laterOf resolves an item's LastActivityAt from its update time and the
// timestamp of its newest log, which is absent for items that have never been
// logged against.
//
// lastLogAt arrives as a string rather than a time: the driver only parses
// columns declared DATETIME, and MAX() erases that declaration. Logs are always
// written by SQLite's CURRENT_TIMESTAMP default, so the format is fixed and UTC.
// A log whose timestamp cannot be parsed is treated as no log at all, leaving
// UpdatedAt as the activity time — a stale-looking item is recoverable, a failed
// query is not.
func laterOf(updatedAt time.Time, lastLogAt sql.NullString) time.Time {
	if !lastLogAt.Valid {
		return updatedAt
	}
	logged, err := time.ParseInLocation(sqliteTimestamp, lastLogAt.String, time.UTC)
	if err != nil || !logged.After(updatedAt) {
		return updatedAt
	}
	return logged
}

// sqliteTimestamp is the layout SQLite's CURRENT_TIMESTAMP writes.
const sqliteTimestamp = "2006-01-02 15:04:05"

func (db *DB) queryItems(query string, args ...any) ([]model.Item, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []model.Item
	for rows.Next() {
		var item model.Item
		var parentID, definitionOfDone sql.NullString
		var lastLogAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Project, &item.Type, &item.Title, &item.Description, &definitionOfDone,
			&item.Status, &item.Priority, &parentID, &item.CreatedAt, &item.UpdatedAt, &lastLogAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan item: %w", err)
		}
		if parentID.Valid {
			item.ParentID = &parentID.String
		}
		if definitionOfDone.Valid {
			item.DefinitionOfDone = &definitionOfDone.String
		}
		item.LastActivityAt = laterOf(item.UpdatedAt, lastLogAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Derive epic status from children at query time
	for i := range items {
		if err := db.applyDerivedEpicStatus(&items[i]); err != nil {
			return nil, err
		}
	}

	return items, nil
}
