// Package logbuf keeps the last N log lines in memory so the admin page can
// show them, without standing up a log aggregator for a single-process app.
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// Buffer is an io.Writer ring buffer of log lines, safe for concurrent use.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func New(max int) *Buffer {
	return &Buffer{max: max}
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, strings.TrimRight(string(p), "\n"))
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = b.lines[over:]
	}
	return len(p), nil
}

// Lines returns a snapshot, oldest first.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

// Tee is an slog.Handler that forwards every record to two handlers, e.g. a
// JSON handler on stdout and a text handler backed by a Buffer.
type Tee struct {
	a, b slog.Handler
}

func NewTee(a, b slog.Handler) *Tee { return &Tee{a: a, b: b} }

func (t *Tee) Enabled(ctx context.Context, level slog.Level) bool {
	return t.a.Enabled(ctx, level) || t.b.Enabled(ctx, level)
}

func (t *Tee) Handle(ctx context.Context, r slog.Record) error {
	if t.a.Enabled(ctx, r.Level) {
		if err := t.a.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	if t.b.Enabled(ctx, r.Level) {
		if err := t.b.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tee) WithAttrs(attrs []slog.Attr) slog.Handler {
	return NewTee(t.a.WithAttrs(attrs), t.b.WithAttrs(attrs))
}

func (t *Tee) WithGroup(name string) slog.Handler {
	return NewTee(t.a.WithGroup(name), t.b.WithGroup(name))
}
