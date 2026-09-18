// Package classify decides whether an inbound email is a newsletter.
//
// Fixed decision order — a subject match on confirm/verify/activate always wins
// and marks the email transactional regardless of headers; failing that, list
// headers resolve most of the rest with no model call; only the remainder
// reaches the LLM, and only ever with subject + sender, never the body.
package classify

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/Safecast/quicky/internal/llm"
	"codeberg.org/Safecast/quicky/internal/prompts"
)

type Class string

const (
	Newsletter    Class = "newsletter"
	Transactional Class = "transactional"
)

type Result struct {
	Class             Class
	NeedsConfirmation bool
	Notes             string
}

type Input struct {
	FromEmail  string
	Subject    string
	HTML       string
	ListID     string
	ListUnsub  string
	Precedence string
}

var confirmRe = regexp.MustCompile(`(?i)\b(confirm|verify|activate)\w*\b`)

// confirmLinkRe finds a subscription-confirmation link in the body so the
// dashboard can surface it.
var confirmLinkRe = regexp.MustCompile(`(?i)href=["']([^"']*(?:confirm|verify|activate)[^"']*)["']`)

func Classify(ctx context.Context, c llm.Client, model string, in Input) (Result, error) {
	if confirmRe.MatchString(in.Subject) {
		return Result{Class: Transactional, NeedsConfirmation: true, Notes: "subject-confirm"}, nil
	}
	if in.ListID != "" || in.ListUnsub != "" || strings.EqualFold(strings.TrimSpace(in.Precedence), "bulk") {
		return Result{Class: Newsletter, Notes: "list-headers"}, nil
	}

	subject := in.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	out, err := c.Text(ctx, llm.Request{
		Model:     model,
		MaxTokens: 10,
		User:      fmt.Sprintf(prompts.ClassifierUser, in.FromEmail, subject),
	})
	if err != nil {
		return Result{}, err
	}
	if strings.Contains(strings.ToLower(out), "not_newsletter") {
		return transactional(in.HTML, "llm"), nil
	}
	if strings.Contains(strings.ToLower(out), "newsletter") {
		return Result{Class: Newsletter, Notes: "llm"}, nil
	}
	return transactional(in.HTML, "llm-unparseable:"+strings.TrimSpace(out)), nil
}

// transactional scans the body for a confirmation link before giving up on the
// email — a confirmation that never mentioned it in the subject still belongs on
// the user's dashboard.
func transactional(html, notes string) Result {
	return Result{
		Class:             Transactional,
		NeedsConfirmation: confirmLinkRe.MatchString(html),
		Notes:             notes,
	}
}
