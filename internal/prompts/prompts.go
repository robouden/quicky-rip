// Package prompts holds the pipeline's prompts verbatim. They are the spec:
// edit the .txt files, not paraphrases in calling code.
package prompts

import _ "embed"

//go:embed parser_system.txt
var ParserSystem string

//go:embed synthesis_system.txt
var SynthesisSystem string

//go:embed relevance_system.txt
var RelevanceSystem string

// ClassifierUser is the LLM-fallback classifier prompt (no system prompt).
// Args: from_email, subject (use "(no subject)" when empty).
const ClassifierUser = `Classify this email as a newsletter/mailing list or not.
Sender: %s
Subject: %s

Reply with exactly one word: "newsletter" or "not_newsletter".`

// SelfContainmentUser is the per-quote cold-reader check. Arg: quote text.
const SelfContainmentUser = `You are a cold reader with no knowledge of the newsletter, author, or topic this quote came from. Your only job is to evaluate whether this quote makes sense on its own.

Ask yourself: if someone showed you this quote with no other context, would you immediately understand what it is about — who or what is being referred to, what claim or observation is being made?

If the meaning is clear and self-contained: reply PASS
If the meaning depends on context you don't have: reply FAIL

Quote: "%s"

Reply with a single word: PASS or FAIL`
