// Package htmlx turns newsletter HTML into the parser's inputs: cleaned text,
// image candidates, and decoded links. This is the largest piece of work in a Go
// port — there is no cheerio/readability to lean on.
//
// The output text is what reaches the parser prompt, so anchors are rendered
// inline as [text](url): the EXTRACT_TOOL schema asks the model to return a url
// alongside its anchor text, which it can only do if both are in front of it.
package htmlx

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Candidate is one image offered to the parser's featured-image rules. Position
// is the 0-based document order the prompt's "[N]" refers to.
type Candidate struct {
	Position int
	Src      string
	Alt      string
	LinkURL  string
}

type Extracted struct {
	Text       string
	Candidates []Candidate
}

// dropped elements never carry editorial content.
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Noscript: true,
	atom.Nav: true, atom.Footer: true, atom.Template: true, atom.Iframe: true,
	atom.Form: true, atom.Button: true, atom.Select: true, atom.Svg: true,
}

// blockLevel elements force a paragraph break so sentences from different
// sections don't run together into one.
var blockLevel = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Br: true, atom.Tr: true, atom.Li: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Table: true, atom.Blockquote: true, atom.Section: true,
	atom.Article: true, atom.Header: true, atom.Hr: true,
}

// Extract cleans HTML to editorial text and collects image candidates.
func Extract(src string) (Extracted, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return Extracted{}, err
	}
	e := &extractor{}
	e.walk(doc, "")
	// Renumber: nested extractors number from zero within their own subtree, so
	// document order is only correct after the tree is flattened.
	for i := range e.candidates {
		e.candidates[i].Position = i
	}
	return Extracted{Text: normalize(e.b.String()), Candidates: e.candidates}, nil
}

type extractor struct {
	b          strings.Builder
	candidates []Candidate
}

// walk renders the tree depth-first. link carries the nearest enclosing <a>
// href, so an image wrapped in a link knows its own link_url.
func (e *extractor) walk(n *html.Node, link string) {
	switch n.Type {
	case html.TextNode:
		e.b.WriteString(n.Data)
		return
	case html.ElementNode:
		if dropped[n.DataAtom] || hidden(n) {
			return
		}
		switch n.DataAtom {
		case atom.A:
			e.anchor(n)
			return
		case atom.Img:
			e.image(n, link)
			return
		}
		if blockLevel[n.DataAtom] {
			e.b.WriteString("\n")
		}
		defer func() {
			if blockLevel[n.DataAtom] {
				e.b.WriteString("\n")
			}
		}()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		e.walk(c, link)
	}
}

// anchor renders as [text](href), or as bare text when there is no usable href.
// An <a> wrapping only an image contributes no text, just the image's link_url.
func (e *extractor) anchor(n *html.Node) {
	href := attr(n, "href")
	var inner extractor
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		inner.walk(c, href)
	}
	e.candidates = append(e.candidates, inner.candidates...)

	text := strings.TrimSpace(collapse(inner.b.String()))
	switch {
	case text == "":
		return
	case href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:"):
		e.b.WriteString(" " + text + " ")
	default:
		e.b.WriteString(" [" + text + "](" + href + ") ")
	}
}

func (e *extractor) image(n *html.Node, link string) {
	src := attr(n, "src")
	if src == "" || strings.HasPrefix(src, "data:") {
		return
	}
	e.candidates = append(e.candidates, Candidate{
		Position: len(e.candidates),
		Src:      src,
		Alt:      strings.TrimSpace(attr(n, "alt")),
		LinkURL:  link,
	})
}

// hidden catches the tracking pixels and preheader text newsletters hide with
// inline styles — they are not editorial content and pollute both the text and
// the image candidate list.
func hidden(n *html.Node) bool {
	style := strings.ToLower(strings.ReplaceAll(attr(n, "style"), " ", ""))
	for _, s := range []string{"display:none", "visibility:hidden", "max-height:0", "opacity:0"} {
		if strings.Contains(style, s) {
			return true
		}
	}
	if attr(n, "hidden") != "" || strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return true
	}
	// A 1x1 image is a tracking pixel.
	return n.DataAtom == atom.Img && attr(n, "width") == "1" && attr(n, "height") == "1"
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// collapse reduces every run of whitespace to a single space.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// spaceBeforePunct undoes the padding anchors add around themselves when a link
// ends a clause: "mail me ." reads as a typo to the model.
var spaceBeforePunct = regexp.MustCompile(`\s+([.,;:!?)\]])`)

// normalize collapses horizontal whitespace within lines while keeping the
// block structure as blank-line-separated paragraphs.
func normalize(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if l := spaceBeforePunct.ReplaceAllString(collapse(line), "$1"); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n\n")
}

// RenderCandidates formats the image candidate block exactly as the parser
// prompt expects it. Returns "" when there are none, so the block is omitted
// rather than sent empty.
func RenderCandidates(cs []Candidate) string {
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nImage candidates from the newsletter HTML:")
	for _, c := range cs {
		b.WriteString(fmt.Sprintf("\n[%d] src: %s\n    alt: %s\n    wrapping link: %s",
			c.Position, c.Src, or(c.Alt, "(none)"), or(c.LinkURL, "(none)")))
	}
	return b.String()
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
