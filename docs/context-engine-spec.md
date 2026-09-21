# Context Engine (SQLite-authoritative)

> **Status: this document supersedes the earlier "Index + Markdown Learnings" proposal.**
> Canonical learning content lives in SQLite (`~/.prog/prog.db`), not in repo-local
> Markdown pages. The Markdown-canonical design below is retained only as historical
> context and must not be treated as the product contract.

## Authoritative model

- **Store**: SQLite via `prog` (`learnings`, `concepts`, `learning_sources`,
  `learning_relations`, FTS5).
- **Unit of knowledge**: a learning (`lrn-…`) with summary, optional detail,
  concepts, typed file evidence (path + optional line range + note), and optional
  task provenance.
- **Retrieval**: one ranked query path (`SearchKnowledge`) combining FTS5, concept
  tags/summaries, task title/description, and file-path evidence. Every hit carries
  explicit match reasons; aggregate scores are internal only.
- **Supersession**: `prog learn supersede <old-id> <new-id>` inserts a typed
  `supersedes` relation and marks the old learning stale in one transaction.
- **CLI**: `prog context --task`, `-q`, `-c`, `--id`, `--full`, `--limit` (default 10),
  `--all`, `--include-stale`, and `--json` map onto that model. Ranked and unscoped
  listing default to one-line summaries; full bodies require `--full` or `--id`.
  Bare unscoped listing is refused; `--all` is the explicit opt-in and still honors
  the default cap unless `--limit` is raised. `--summary` remains accepted as a
  compatibility no-op. Human and JSON output share selection semantics and expose
  `total` / truncation clearly; they do not expose an unexplained score.

## Embedding decision

Embeddings (and RRF hybrid ranking) are **rejected for now**.

`TestSearchKnowledge` in `internal/db/learnings_test.go` covers the literal
retrieval cases required by the epic: exact summary, detail/FTS, concept name,
concept summary, file path, task-context ranking, concept-only query, stale
exclusion, no-result, deterministic ordering, and negative cases where generic
or unrelated tokens must not match (`ui` vs `build`, generic "fix the bug").

No failing semantic case remains in that suite that lexical + structured signals
do not already satisfy. Adding embeddings would impose embedding-model, index,
and refresh operational cost without a measured gap to close. Revisit only if a
new retrieval case fails under the current ranked path.

---

## Historical proposal (superseded): Index + Markdown Learnings

The text below described an abandoned design that kept canonical learnings in
repo-local Markdown and treated `prog` as an index of stubs. It conflicts with
the SQLite-authoritative model above and is not implemented.

### Summary (historical)
Move canonical learnings into repo-local Markdown pages and use `prog` as a fast indexing layer. Agents see lightweight stubs (one-liner + short summary + path) and pull full pages only on demand.

### Goals (historical)
- Keep canonical knowledge in Markdown files inside the working repo.
- Keep agent context light via stubs, with explicit on-demand expansion.
- Make discovery and updates fast for agents ("always available").
- Preserve provenance and quality via indexing metadata.

### Non-goals (historical)
- `prog` is not the source of truth for learning content.
- Do not require a centralized knowledge base outside the repo.
- No implicit full-content loading without explicit signal or high confidence.

These non-goals are **void**: SQLite *is* the source of truth for learning content.
