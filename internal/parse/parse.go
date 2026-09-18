// Package parse is the per-email extraction stage: the highest-frequency LLM
// call in the pipeline, and therefore the dominant cost driver. Skipped
// entirely for users past their trial with no subscription — that check belongs
// before the job is enqueued, not here.
package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"codeberg.org/Safecast/quicky/internal/htmlx"
	"codeberg.org/Safecast/quicky/internal/llm"
	"codeberg.org/Safecast/quicky/internal/prompts"
)

// ParsedNewsletter mirrors the EXTRACT_TOOL schema.
type ParsedNewsletter struct {
	Summary            string              `json:"summary"`
	IsOnboarding       bool                `json:"is_onboarding"`
	ViewOnlineURL      *string             `json:"view_online_url"`
	Quotes             []Quote             `json:"quotes"`
	Links              []Link              `json:"links"`
	TimeSensitiveItems []TimeSensitiveItem `json:"time_sensitive_items"`
	Topics             []string            `json:"topics"`
	FeaturedImage      *FeaturedImage      `json:"featured_image,omitempty"`
}

type Quote struct {
	Text        string `json:"text"`
	Attribution string `json:"attribution,omitempty"`
	IsOpinion   bool   `json:"is_opinion"`
}

type Link struct {
	URL         string `json:"url"`
	AnchorText  string `json:"anchor_text"`
	Context     string `json:"context"`
	IsEditorial bool   `json:"is_editorial"`
}

type TimeSensitiveItem struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	EventDate   string `json:"event_date"` // ISO 8601
	URL         string `json:"url,omitempty"`
}

type FeaturedImage struct {
	Src     string  `json:"src"`
	LinkURL *string `json:"link_url,omitempty"`
}

// ExtractTool is the forced tool the parser's output is shaped by.
var ExtractTool = llm.Tool{
	Name:        "extract_newsletter",
	Description: "Return the structured extraction of this newsletter.",
	InputSchema: json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "summary": {"type": "string"},
	    "is_onboarding": {"type": "boolean"},
	    "quotes": {"type": "array", "items": {"type": "object",
	      "properties": {"text": {"type": "string"}, "attribution": {"type": "string"}, "is_opinion": {"type": "boolean"}},
	      "required": ["text", "is_opinion"]}},
	    "links": {"type": "array", "items": {"type": "object",
	      "properties": {"url": {"type": "string"}, "anchor_text": {"type": "string"}, "context": {"type": "string"}, "is_editorial": {"type": "boolean"}},
	      "required": ["url", "anchor_text", "context", "is_editorial"]}},
	    "time_sensitive_items": {"type": "array", "items": {"type": "object",
	      "properties": {"title": {"type": "string"}, "description": {"type": "string"}, "event_date": {"type": "string"}, "url": {"type": "string"}},
	      "required": ["title", "event_date"]}},
	    "view_online_url": {"type": ["string", "null"]},
	    "featured_image": {"type": ["object", "null"],
	      "properties": {"src": {"type": "string"}, "link_url": {"type": ["string", "null"]}},
	      "required": ["src"]},
	    "topics": {"type": "array", "items": {"type": "string"}, "minItems": 3, "maxItems": 5}
	  },
	  "required": ["summary", "is_onboarding", "quotes", "links", "time_sensitive_items", "view_online_url", "topics"]
	}`),
}

// linkWorkers bounds concurrent redirect resolution. A newsletter carries
// 30-80 links and this is the hottest stage: resolving them serially would put
// minutes of external latency on every email.
const linkWorkers = 12

type Parser struct {
	LLM   llm.Client
	Model string
	// HC resolves tracking redirects. Nil skips the network entirely and
	// unwraps by query parameter only.
	HC *http.Client
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// Run extracts one newsletter. Quotes that fail the cold-reader check are
// dropped, and only editorial links are kept and canonicalized.
func (p *Parser) Run(ctx context.Context, subject, rawHTML string) (ParsedNewsletter, error) {
	var out ParsedNewsletter

	ex, err := htmlx.Extract(rawHTML)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(ex.Text) == "" {
		return out, fmt.Errorf("parse: no extractable text")
	}

	if err := p.LLM.Tool(ctx, llm.Request{
		Model:       p.Model,
		MaxTokens:   4096,
		System:      prompts.ParserSystem,
		CacheSystem: true,
		User:        userPrompt(p.today(), subject, ex),
	}, ExtractTool, &out); err != nil {
		return out, err
	}

	out.Quotes, err = p.keepSelfContained(ctx, out.Quotes)
	if err != nil {
		return out, err
	}
	out.Links = p.editorialLinks(ctx, out.Links)
	return out, nil
}

func (p *Parser) today() string {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	return now().Format("2006-01-02")
}

// userPrompt renders the parser's user message. The candidates block is omitted
// entirely when there are no images, per the prompt's own contract.
//
// The date line is an addition to the documented template: the system prompt's
// time-sensitive rule ("within the next 7 days", "register by Saturday") cannot
// be applied without it, and a live run without it produced a date a year off.
func userPrompt(today, subject string, ex htmlx.Extracted) string {
	var b strings.Builder
	b.WriteString("Today's date: " + today + "\n\n")
	if subject != "" {
		b.WriteString("Subject: " + subject + "\n\n")
	}
	b.WriteString("Newsletter content:\n\n")
	b.WriteString(ex.Text)
	b.WriteString(htmlx.RenderCandidates(ex.Candidates))
	return b.String()
}

// keepSelfContained runs the per-quote cold-reader check. Only the model's
// verdict drops a quote: a transport error (rate limit, timeout) is returned so
// the whole parse rolls back and retries, rather than silently persisting a
// quote-less email with no record of why.
func (p *Parser) keepSelfContained(ctx context.Context, qs []Quote) ([]Quote, error) {
	kept := make([]Quote, 0, len(qs))
	for _, q := range qs {
		verdict, err := p.LLM.Text(ctx, llm.Request{
			Model:     p.Model,
			MaxTokens: 100,
			User:      fmt.Sprintf(prompts.SelfContainmentUser, q.Text),
		})
		if err != nil {
			return nil, fmt.Errorf("parse: self-containment check: %w", err)
		}
		if strings.Contains(strings.ToUpper(verdict), "PASS") {
			kept = append(kept, q)
		}
	}
	return kept, nil
}

// editorialLinks drops everything the model did not mark editorial, then
// resolves the survivors through a bounded pool. The canonical destination is
// what gets stored — never the tracking wrapper.
func (p *Parser) editorialLinks(ctx context.Context, links []Link) []Link {
	kept := make([]Link, 0, len(links))
	for _, l := range links {
		if l.IsEditorial && l.URL != "" {
			kept = append(kept, l)
		}
	}

	sem := make(chan struct{}, linkWorkers)
	var wg sync.WaitGroup
	for i := range kept {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			kept[i].URL = htmlx.Canonical(ctx, p.HC, kept[i].URL)
		}(i)
	}
	wg.Wait()
	return kept
}
