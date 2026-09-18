package parse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codeberg.org/Safecast/quicky/internal/llm"
)

type stub struct {
	toolReq  llm.Request
	toolOut  ParsedNewsletter
	verdicts map[string]string // quote text -> PASS/FAIL
	textReqs []llm.Request
}

func (s *stub) Tool(_ context.Context, req llm.Request, _ llm.Tool, out any) error {
	s.toolReq = req
	b, _ := json.Marshal(s.toolOut)
	return json.Unmarshal(b, out)
}

func (s *stub) Text(_ context.Context, req llm.Request) (string, error) {
	s.textReqs = append(s.textReqs, req)
	for q, v := range s.verdicts {
		if strings.Contains(req.User, q) {
			return v, nil
		}
	}
	return "FAIL", nil
}

const page = `<p>The FDA <a href="https://fda.test/g">guidance</a> lands Monday.</p>
<a href="https://art.test/p"><img src="https://img.test/p.jpg" alt="Portrait of the artist"></a>`

func TestRun(t *testing.T) {
	s := &stub{
		toolOut: ParsedNewsletter{
			Summary: "s",
			Quotes: []Quote{
				{Text: "Regulation is a lagging indicator of harm.", IsOpinion: true},
				{Text: "This changes everything.", IsOpinion: true},
			},
			Links: []Link{
				{URL: "https://click.test/c?u=https%3A%2F%2Ffda.test%2Fg", AnchorText: "guidance", IsEditorial: true},
				{URL: "https://news.test/unsubscribe", AnchorText: "Unsubscribe", IsEditorial: false},
			},
			Topics: []string{"a", "b", "c"},
		},
		verdicts: map[string]string{
			"Regulation is a lagging": "PASS",
			"This changes":            "FAIL",
		},
	}
	p := &Parser{LLM: s, Model: "haiku", Now: func() time.Time { return time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC) }} // HC nil: no network
	out, err := p.Run(context.Background(), "Issue #12", page)
	if err != nil {
		t.Fatal(err)
	}

	// Prompt assembly.
	if !s.toolReq.CacheSystem {
		t.Error("parser system prompt must be cached: it is the dominant cost driver")
	}
	for _, want := range []string{
		"Today's date: 2026-09-18",
		"Subject: Issue #12",
		"Newsletter content:",
		"[guidance](https://fda.test/g)",
		"Image candidates from the newsletter HTML:",
		"[0] src: https://img.test/p.jpg",
		"wrapping link: https://art.test/p",
	} {
		if !strings.Contains(s.toolReq.User, want) {
			t.Errorf("user prompt missing %q:\n%s", want, s.toolReq.User)
		}
	}

	// Quote gate: one PASS, one FAIL.
	if len(out.Quotes) != 1 || !strings.HasPrefix(out.Quotes[0].Text, "Regulation") {
		t.Errorf("quote gate wrong: %+v", out.Quotes)
	}
	if len(s.textReqs) != 2 || s.textReqs[0].MaxTokens != 100 {
		t.Errorf("self-containment check must run once per quote at max_tokens 100: %+v", s.textReqs)
	}

	// Links: non-editorial dropped, editorial canonicalized.
	if len(out.Links) != 1 || out.Links[0].URL != "https://fda.test/g" {
		t.Errorf("links wrong: %+v", out.Links)
	}
}

func TestRunRejectsEmptyContent(t *testing.T) {
	p := &Parser{LLM: &stub{}, Model: "haiku"}
	if _, err := p.Run(context.Background(), "", "<style>x</style>"); err == nil {
		t.Error("an email with no extractable text must not be sent to the model")
	}
}

func TestNoCandidatesBlockWithoutImages(t *testing.T) {
	s := &stub{toolOut: ParsedNewsletter{Topics: []string{"a", "b", "c"}}}
	p := &Parser{LLM: s, Model: "haiku"}
	if _, err := p.Run(context.Background(), "", "<p>hello</p>"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.toolReq.User, "Image candidates") {
		t.Error("candidates block must be omitted when there are no images")
	}
}

type failingText struct{ stub }

func (f *failingText) Text(context.Context, llm.Request) (string, error) {
	return "", context.DeadlineExceeded
}

func TestTransportErrorInQuoteGateFailsTheParse(t *testing.T) {
	s := &failingText{stub{toolOut: ParsedNewsletter{
		Quotes: []Quote{{Text: "A quote.", IsOpinion: true}},
		Topics: []string{"a", "b", "c"},
	}}}
	p := &Parser{LLM: s, Model: "haiku"}
	if _, err := p.Run(context.Background(), "", "<p>hello</p>"); err == nil {
		t.Error("a rate limit must retry the parse, not silently drop the quote")
	}
}
