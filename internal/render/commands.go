// internal/render/commands.go
package render

// Command is a curated review-action button shown beneath the verdict bar. Clicking it ends
// the review round and returns {verdict:"command", command, prompt} to the calling agent.
type Command struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Prompt      string `json:"prompt"`
	Recommended bool   `json:"recommended,omitempty"` // presentational only: highlight the pill
}

// BuiltinCommands is the curated default set shown when the caller supplies no MDVIEW_COMMANDS.
func BuiltinCommands() []Command {
	return []Command{
		{ID: "review-with-subagent", Label: "Review with subagent", Prompt: "Dispatch an independent subagent to review this document for gaps, risks, and weak assumptions, and report its findings before proceeding."},
		{ID: "devils-advocate", Label: "Stress-test", Prompt: "Challenge this document: surface edge cases, failure modes, and unstated assumptions that could break it, before I proceed."},
		{ID: "simplify", Label: "Simplify", Prompt: "Simplify and tighten this — cut unnecessary scope and complexity without losing essential content — then re-render for review."},
		{ID: "add-detail", Label: "Add detail", Prompt: "Expand the thin or vague parts of this with concrete specifics, then re-render for review."},
		{ID: "verify-against-codebase", Label: "Verify vs codebase", Prompt: "Verify this against the actual codebase — confirm the files, APIs, types, and assumptions it references exist and are accurate, and flag any drift before I proceed."},
		{ID: "explore-alternatives", Label: "Explore alternatives", Prompt: "Lay out 2–3 alternative approaches to what this proposes, with tradeoffs and a recommendation, before I commit to this one."},
	}
}
