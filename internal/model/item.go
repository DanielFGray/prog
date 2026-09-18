// Package model defines the core data types for the tasks system.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// GenerateID returns a new ID with a type-specific prefix and 6 hex chars.
//
// Prefixes by item type:
//   - task: "ts-" (e.g., ts-a1b2c3)
//   - epic: "ep-" (e.g., ep-a1b2c3)
func GenerateID(itemType ItemType) string {
	prefix := "ts-"
	if itemType == ItemTypeEpic {
		prefix = "ep-"
	}
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

type ItemType string

const (
	ItemTypeTask ItemType = "task"
	ItemTypeEpic ItemType = "epic"
)

func (t ItemType) IsValid() bool {
	return t == ItemTypeTask || t == ItemTypeEpic
}

type Status string

const (
	StatusDraft      Status = "draft" // Task is being defined/scoped, not yet ready for work
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked" // Derived state only — set by dep resolution and epic derivation, not by CLI
	StatusReviewing  Status = "reviewing"
	StatusDone       Status = "done"
	StatusCanceled   Status = "canceled"
)

func (s Status) IsValid() bool {
	return s == StatusDraft || s == StatusOpen || s == StatusInProgress || s == StatusBlocked || s == StatusReviewing || s == StatusDone || s == StatusCanceled
}

// Item represents a task or epic in the system.
type Item struct {
	ID               string   // Unique identifier (ts-XXXXXX or ep-XXXXXX)
	Project          string   // Project scope (e.g., "gaia", "myapp")
	Type             ItemType // "task" or "epic"
	Title            string   // Short description
	Description      string   // Full context, notes, handoff info
	DefinitionOfDone *string  // Completion criteria for agents (natural language)
	Status           Status   // Current state
	Priority         int      // 1=high, 2=medium, 3=low
	ParentID         *string  // Optional parent epic ID
	Labels           []string // Attached label names (populated separately)
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// LastActivityAt is the later of UpdatedAt and the item's newest log entry.
	// Derived at query time and never stored: adding a log is activity on the
	// item, but logs live in their own table and do not touch items.updated_at.
	// Deciding whether a claim has gone stale needs this, not UpdatedAt, which
	// on an in-progress task only records when the task was claimed.
	LastActivityAt time.Time
}

// Log is a timestamped audit trail entry for an item.
type Log struct {
	ID        int64
	ItemID    string
	Message   string
	CreatedAt time.Time
}

// Dep represents a dependency relationship where ItemID depends on DependsOn.
// ItemID is blocked until DependsOn reaches a terminal status ("done" or "canceled").
type Dep struct {
	ItemID    string
	DependsOn string
}

// Project represents a named project that groups related items.
type Project struct {
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// LearningStatus represents the lifecycle state of a learning.
type LearningStatus string

const (
	LearningStatusActive   LearningStatus = "active"
	LearningStatusStale    LearningStatus = "stale"
	LearningStatusArchived LearningStatus = "archived"
)

func (s LearningStatus) IsValid() bool {
	return s == LearningStatusActive || s == LearningStatusStale || s == LearningStatusArchived
}

// Concept represents a knowledge category. Concepts are global: a name
// identifies the same concept regardless of which project the learnings
// filed under it came from.
type Concept struct {
	ID            string // con-XXXXXX
	Name          string
	Summary       string
	LastUpdated   time.Time
	LearningCount int // Derived from learning_concepts join
}

// LearningSource is a file reference attached to a learning, with optional
// line bounds and note. Path is required. Line shapes:
//   - neither bound set: whole file
//   - start only: exact line
//   - both bounds: inclusive range (end >= start, both positive)
type LearningSource struct {
	ID        string // src-XXXXXX
	Path      string
	StartLine *int
	EndLine   *int
	Note      string
}

// LearningRelationKind is the typed edge between two learnings.
// Only supersedes is supported; no generic graph kinds.
type LearningRelationKind string

const LearningRelationKindSupersedes LearningRelationKind = "supersedes"

// LearningRelation is a directed learning-to-learning edge.
// For kind supersedes: SourceID is the replacement, TargetID is the learning it replaces.
type LearningRelation struct {
	SourceID string
	TargetID string
	Kind     LearningRelationKind
}

// Learning represents a piece of knowledge discovered during work.
type Learning struct {
	ID        string // lrn-XXXXXX
	CreatedAt time.Time
	UpdatedAt time.Time
	TaskID    *string // Optional link to the task that discovered this
	Summary   string  // One-liner
	Detail    string  // Full context
	Sources   []LearningSource
	Files     []string // Derived from Sources paths for non-DB callers
	Status    LearningStatus
	Concepts  []string // Associated concept names
	// Relations are edges where this learning is source or target.
	Relations []LearningRelation
}

// GenerateLearningID returns a new learning ID with lrn- prefix and 6 hex chars.
func GenerateLearningID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "lrn-" + hex.EncodeToString(b)
}

// GenerateLearningSourceID returns a new source ID with src- prefix and 6 hex chars.
func GenerateLearningSourceID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "src-" + hex.EncodeToString(b)
}

// Match signal identifiers for KnowledgeHit reasons. Stable user-facing contract:
// reasons name the signal that fired; they do not expose an aggregate score.
const (
	MatchExactSummary    = "exact_summary"
	MatchFTSSummary      = "fts_summary"
	MatchFTSDetail       = "fts_detail"
	MatchConceptName     = "concept_name"
	MatchConceptSummary  = "concept_summary"
	MatchTaskTitle       = "task_title"
	MatchTaskDescription = "task_description"
	MatchFilePath        = "file_path"
)

// KnowledgeQuery is the input to ranked knowledge retrieval.
// Text drives FTS, exact-summary, discovered-concept, and file-path signals.
// Concepts selects learnings by tag without requiring those names in Text or task prose.
// TaskID, when set, loads that item's title and description as task-context signals.
type KnowledgeQuery struct {
	Text         string
	TaskID       string
	Concepts     []string
	IncludeStale bool
}

// MatchReason identifies one retrieval signal that contributed to a hit.
type MatchReason struct {
	Signal string // one of the Match* constants
	Detail string // specific matched value (concept name, file path, phrase, …)
}

// KnowledgeHit is a ranked learning with explicit match reasons.
type KnowledgeHit struct {
	Learning Learning
	Reasons  []MatchReason
}

// GenerateConceptID returns a new concept ID with con- prefix and 6 hex chars.
func GenerateConceptID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "con-" + hex.EncodeToString(b)
}

// Label represents a tag that can be attached to items for categorization.
// Labels are project-scoped and identified by name (IDs are internal).
type Label struct {
	ID        string // lbl-XXXXXX (internal)
	Name      string // User-facing identifier, unique per project
	Project   string
	Color     string // Optional hex color for UI display
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GenerateLabelID returns a new label ID with lbl- prefix and 6 hex chars.
func GenerateLabelID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "lbl-" + hex.EncodeToString(b)
}
