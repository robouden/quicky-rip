// Package ingest is the Mailgun inbound webhook. It does no LLM work: it
// verifies, stores, and returns 200. Everything downstream is picked up by the
// queue workers off the durable row.
package ingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"codeberg.org/Safecast/quicky/internal/store"
)

type Handler struct {
	DB         *store.DB
	SigningKey string
	Log        *slog.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
	}
	f := r.PostForm
	if !validSignature(h.SigningKey, f.Get("timestamp"), f.Get("token"), f.Get("signature")) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}

	recipient := f.Get("recipient")
	addr, tag := splitPlusTag(recipient)

	u, err := h.DB.UserByInboundAddress(r.Context(), addr)
	if err != nil {
		// Unknown address: 200 so Mailgun stops retrying a message that can
		// never be routed.
		h.Log.Warn("inbound for unknown address", "recipient", recipient)
		w.WriteHeader(http.StatusOK)
		return
	}

	var plusTag *string
	if tag != "" {
		plusTag = &tag
	}
	hdr := headers(f)
	senderID, err := h.senderID(r.Context(), u.ID, f, hdr)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	id, inserted, err := h.DB.InsertInbound(r.Context(), store.InboundEmail{
		UserID:     u.ID,
		SenderID:   senderID,
		MessageID:  messageID(f, hdr),
		ListID:     nilIfEmpty(hdr["list-id"]),
		ListUnsub:  nilIfEmpty(hdr["list-unsubscribe"]),
		Precedence: nilIfEmpty(hdr["precedence"]),
		Subject:    f.Get("subject"),
		FromEmail:  addressOf(f.Get("from")),
		FromName:   displayNameOf(f.Get("from")),
		Recipient:  recipient,
		PlusTag:    plusTag,
		HTML:       f.Get("body-html"),
		Text:       f.Get("body-plain"),
	})
	if err != nil {
		// Only now may we fail: a 500 makes Mailgun retry, which the unique
		// constraint turns into a harmless no-op. Returning 200 here would lose
		// the email silently.
		h.Log.Error("insert inbound failed", "err", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	h.Log.Info("ingested", "id", id, "inserted", inserted, "user", u.ID)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) senderID(ctx context.Context, userID string, f map[string][]string, hdr map[string]string) (*string, error) {
	from := formValue(f, "from")
	// The senders copy is a newest-seen denormalization for the dashboard's
	// unsubscribe link only. Classification never reads it.
	id, err := h.DB.UpsertSender(ctx, userID, addressOf(from), displayNameOf(from),
		nilIfEmpty(hdr["list-id"]), nilIfEmpty(hdr["list-unsubscribe"]), nilIfEmpty(hdr["list-unsubscribe-post"]))
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// headers flattens the message headers, lowercased. Mailgun forwards most
// headers as individual form fields but not reliably all of them, so the
// message-headers JSON array is the authoritative source and is merged last.
func headers(f map[string][]string) map[string]string {
	out := map[string]string{}
	for k, v := range f {
		if len(v) > 0 && strings.Contains(k, "-") {
			out[strings.ToLower(k)] = v[0]
		}
	}
	var pairs [][]any
	if raw := formValue(f, "message-headers"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &pairs); err == nil {
			for _, p := range pairs {
				if len(p) == 2 {
					name, _ := p[0].(string)
					value, _ := p[1].(string)
					if name != "" {
						out[strings.ToLower(name)] = value
					}
				}
			}
		}
	}
	return out
}

func formValue(f map[string][]string, k string) string {
	if v, ok := f[k]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

func validSignature(key, timestamp, token, signature string) bool {
	if key == "" || timestamp == "" || token == "" || signature == "" {
		return false
	}
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(timestamp + token))
	return hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(signature))
}

// messageID is the idempotency key. It must never be empty: the column is
// NOT NULL UNIQUE, so a second header-less email would hit ON CONFLICT DO
// NOTHING and be dropped while the webhook still returned 200.
func messageID(f map[string][]string, hdr map[string]string) string {
	for _, k := range []string{"Message-Id", "message-id"} {
		if v := formValue(f, k); v != "" {
			return strings.Trim(v, "<>")
		}
	}
	if v := hdr["message-id"]; v != "" {
		return strings.Trim(v, "<>")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		formValue(f, "recipient"), formValue(f, "from"), formValue(f, "subject"),
		formValue(f, "timestamp"), formValue(f, "token"),
	}, "\x00")))
	return "synthetic:" + hex.EncodeToString(sum[:])
}

// splitPlusTag turns "user+tag@quicky.example" into ("user@quicky.example", "tag").
func splitPlusTag(addr string) (string, string) {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr, ""
	}
	local, domain := addr[:at], addr[at:]
	plus := strings.Index(local, "+")
	if plus < 0 {
		return addr, ""
	}
	return local[:plus] + domain, local[plus+1:]
}

func addressOf(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			return strings.TrimSpace(from[i+1 : j])
		}
	}
	return strings.TrimSpace(from)
}

func displayNameOf(from string) string {
	if i := strings.LastIndex(from, "<"); i > 0 {
		return strings.Trim(strings.TrimSpace(from[:i]), `"`)
	}
	return ""
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
