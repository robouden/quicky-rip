package htmlx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// wrapperParams are the query keys tracking redirectors hide the real
// destination in. Unwrapping these costs no network round trip.
var wrapperParams = []string{"url", "u", "target", "redirect", "redirect_uri", "dest", "destination", "link"}

// Canonical resolves a tracking-redirect URL to its destination, which is what
// gets stored — never the tracking wrapper. It unwraps by query parameter
// first, then follows redirects over the network. On any failure it returns the
// input unchanged: a working wrapper URL beats a dropped link.
func Canonical(ctx context.Context, hc *http.Client, raw string) string {
	if u := unwrapParam(raw); u != "" {
		raw = u
	}
	if hc == nil {
		return raw
	}
	final, err := follow(ctx, hc, raw)
	if err != nil {
		return raw
	}
	return final
}

// unwrapParam recursively pulls the destination out of nested wrappers
// (a tracker wrapping a tracker is common in forwarded newsletters).
func unwrapParam(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := u.Query()
	for _, k := range wrapperParams {
		v := q.Get(k)
		if v == "" || !strings.HasPrefix(v, "http") {
			continue
		}
		if inner := unwrapParam(v); inner != "" {
			return inner
		}
		return v
	}
	return ""
}

var errTooManyHops = errors.New("htmlx: redirect limit")

func follow(ctx context.Context, hc *http.Client, raw string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var final string
	client := *hc
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errTooManyHops
		}
		final = req.URL.String()
		return nil
	}
	// HEAD first: most redirectors honour it and it avoids pulling the body.
	// `final` resets per attempt — a tracker's 405 on HEAD is common, and the
	// GET chain must not inherit the abandoned chain's destination.
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		final = raw
		req, err := http.NewRequestWithContext(ctx, method, raw, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			return unwrapOr(final), nil
		}
	}
	return "", errors.New("htmlx: could not resolve " + raw)
}

// unwrapOr strips a wrapper that only appeared after the redirect chain.
func unwrapOr(raw string) string {
	if u := unwrapParam(raw); u != "" {
		return u
	}
	return raw
}
