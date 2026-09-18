// Package synth builds one digest per user per period.
//
// Three call sites share one system prompt and tool: first pass, a structural
// retry when no tool call came back, and a coverage-correction retry.
//
// The coverage check is its own stage between synthesis and the quote gate, with
// its own bounded retry and a write to outcome_alert_log on failure. It is what
// actually enforces zero omission — the synthesis prompt is not trusted to.
package synth

// Quicky mirrors the QUICKY_TOOL schema.
type Quicky struct {
	Paragraphs  []string `json:"paragraphs"`
	SubjectHook string   `json:"subject_hook"`
}

// Relevance mirrors RELEVANCE_TOOL.
type Relevance struct {
	Decisions []struct {
		Index int  `json:"index"`
		Keep  bool `json:"keep"`
	} `json:"decisions"`
}

// TODO: Run — build source list (tagging link-less sources [VOICE NEWSLETTER]),
// site 1, coverage check, site 3 on missing senders, then the quote gate
// (fails open: only a positive mismatch drops a quote).
