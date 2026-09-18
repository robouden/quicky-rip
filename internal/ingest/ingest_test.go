package ingest

import "testing"

func TestSplitPlusTag(t *testing.T) {
	for _, tc := range []struct{ in, addr, tag string }{
		{"rob@in.quicky.test", "rob@in.quicky.test", ""},
		{"rob+news@in.quicky.test", "rob@in.quicky.test", "news"},
		{"rob+a+b@in.quicky.test", "rob@in.quicky.test", "a+b"},
		{"garbage", "garbage", ""},
	} {
		addr, tag := splitPlusTag(tc.in)
		if addr != tc.addr || tag != tc.tag {
			t.Errorf("%q: got (%q,%q), want (%q,%q)", tc.in, addr, tag, tc.addr, tc.tag)
		}
	}
}

func TestFromHeaderParsing(t *testing.T) {
	const h = `"Matt Levine" <money@bloomberg.test>`
	if got := addressOf(h); got != "money@bloomberg.test" {
		t.Errorf("addressOf = %q", got)
	}
	if got := displayNameOf(h); got != "Matt Levine" {
		t.Errorf("displayNameOf = %q", got)
	}
}

func TestHeadersPreferMessageHeadersJSON(t *testing.T) {
	f := map[string][]string{
		"message-headers": {`[["List-Id","<daily.example.com>"],["Precedence","bulk"]]`},
	}
	h := headers(f)
	if h["list-id"] != "<daily.example.com>" || h["precedence"] != "bulk" {
		t.Fatalf("got %+v", h)
	}
}

func TestMessageIDNeverEmpty(t *testing.T) {
	f := map[string][]string{"recipient": {"rob@in.test"}, "timestamp": {"1"}, "token": {"t"}}
	id := messageID(f, headers(f))
	if id == "" {
		t.Fatal("empty message id would silently drop the second header-less email")
	}
	g := map[string][]string{"Message-Id": {"<abc@x.test>"}}
	if got := messageID(g, headers(g)); got != "abc@x.test" {
		t.Fatalf("got %q", got)
	}
}
