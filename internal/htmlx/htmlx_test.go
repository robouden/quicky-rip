package htmlx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sample = `<html><head><style>p{color:red}</style><title>x</title></head>
<body>
<div style="display:none">Preheader teaser text</div>
<img src="https://t.test/open.gif" width="1" height="1" alt="">
<nav><a href="https://n.test/archive">Archive</a></nav>
<h1>The Big Thing</h1>
<p>The FDA's <a href="https://fda.test/guidance">new guidance</a> lands Monday.</p>
<p>Also see <a href="#top">the top</a> and <a href="mailto:a@b.test">mail me</a>.</p>
<a href="https://art.test/piece"><img src="https://img.test/piece.jpg" alt="Portrait of Marina Abramovic in her studio"></a>
<img src="https://img.test/logo.png" alt="Cascadia Journal">
<footer><a href="https://n.test/unsub">Unsubscribe</a></footer>
<script>track()</script>
</body></html>`

func TestExtractText(t *testing.T) {
	got, err := Extract(sample)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The Big Thing",
		"[new guidance](https://fda.test/guidance)",
		"the top", // in-page anchor keeps its text, loses the href
		"mail me",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text missing %q\n---\n%s", want, got.Text)
		}
	}
	for _, unwanted := range []string{
		"p{color:red}",          // <style>
		"track()",               // <script>
		"Preheader teaser text", // display:none
		"Archive",               // <nav>
		"Unsubscribe",           // <footer>
		"(#top)", "(mailto:",    // non-http hrefs are not rendered as links
	} {
		if strings.Contains(got.Text, unwanted) {
			t.Errorf("text should not contain %q\n---\n%s", unwanted, got.Text)
		}
	}
	if strings.Contains(got.Text, "MondayAlso") {
		t.Error("block elements must not run together")
	}
}

func TestExtractCandidates(t *testing.T) {
	got, err := Extract(sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2 (tracking pixel and footer/nav images excluded): %+v", len(got.Candidates), got.Candidates)
	}
	first := got.Candidates[0]
	if first.Position != 0 || first.LinkURL != "https://art.test/piece" {
		t.Errorf("wrapping link not captured: %+v", first)
	}
	if got.Candidates[1].Position != 1 {
		t.Errorf("positions must be document order: %+v", got.Candidates)
	}
	// The logo is still a candidate — rejecting it is the prompt's job, not the
	// extractor's. Extract only removes what is structurally not content.
	if got.Candidates[1].Alt != "Cascadia Journal" {
		t.Errorf("alt text must reach the prompt verbatim: %+v", got.Candidates[1])
	}
}

func TestCanonicalUnwrapsWithoutNetwork(t *testing.T) {
	in := "https://click.test/c?u=" + "https%3A%2F%2Freal.test%2Fpost%3Fid%3D7"
	if got := Canonical(context.Background(), nil, in); got != "https://real.test/post?id=7" {
		t.Errorf("got %q", got)
	}
}

func TestCanonicalFollowsRedirects(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer dest.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/post", http.StatusFound)
	}))
	defer hop.Close()

	got := Canonical(context.Background(), hop.Client(), hop.URL+"/t/abc")
	if got != dest.URL+"/post" {
		t.Errorf("got %q, want %q", got, dest.URL+"/post")
	}
}

func TestCanonicalFailsOpen(t *testing.T) {
	const raw = "https://unreachable.invalid/x"
	if got := Canonical(context.Background(), http.DefaultClient, raw); got != raw {
		t.Errorf("a broken resolve must return the input unchanged, got %q", got)
	}
}

func TestPunctuationAfterLink(t *testing.T) {
	got, err := Extract(`<p>See <a href="https://x.test/a">this</a>.</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "](https://x.test/a).") {
		t.Errorf("link should sit flush against its punctuation: %q", got.Text)
	}
}

func TestRenderCandidates(t *testing.T) {
	if RenderCandidates(nil) != "" {
		t.Error("no candidates must render no block")
	}
	got := RenderCandidates([]Candidate{{Position: 0, Src: "https://i.test/a.jpg"}})
	for _, want := range []string{"[0] src: https://i.test/a.jpg", "alt: (none)", "wrapping link: (none)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
