// Command quicky runs the ingest webhook and the pipeline workers in one
// process. Split them later by passing a role flag; the queue is in the
// database, so they can already run on separate hosts.
package main

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"codeberg.org/Safecast/quicky/internal/admin"
	"codeberg.org/Safecast/quicky/internal/classify"
	"codeberg.org/Safecast/quicky/internal/config"
	"codeberg.org/Safecast/quicky/internal/ingest"
	"codeberg.org/Safecast/quicky/internal/llm"
	"codeberg.org/Safecast/quicky/internal/logbuf"
	"codeberg.org/Safecast/quicky/internal/parse"
	"codeberg.org/Safecast/quicky/internal/queue"
	"codeberg.org/Safecast/quicky/internal/settings"
	"codeberg.org/Safecast/quicky/internal/store"
	"github.com/jackc/pgx/v5"
)

func main() {
	logs := logbuf.New(500)
	handler := logbuf.NewTee(
		slog.NewJSONHandler(os.Stdout, nil),
		slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
	log := slog.New(handler)
	if err := run(log, logs); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, logs *logbuf.Buffer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	sett := settings.New(db, cfg.ModelFast, cfg.ModelStrong)
	ai := llm.NewRouter(llm.New(cfg.AnthropicAPIKey), sett)

	mux := http.NewServeMux()
	mux.Handle("/webhooks/mailgun/inbound", &ingest.Handler{DB: db, SigningKey: cfg.MailgunSigningKey, Log: log})
	adminHandler := &admin.Handler{Settings: sett, Logs: logs, DB: db, Password: cfg.AdminPassword, Log: log}
	mux.Handle("/admin", adminHandler)
	mux.HandleFunc("/admin/logs", adminHandler.LogsHandler)
	mux.Handle("/admin/emails", http.RedirectHandler("/admin#emails", http.StatusFound))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sh)
	}()

	go func() {
		err := queue.Run(ctx, db.Pool, queue.ClaimUnclassified, 10*time.Second,
			classifyJob(ai, cfg.ModelFast), logUnlessShutdown(log, "classify worker"))
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("classify worker stopped", "err", err)
		}
	}()

	parser := &parse.Parser{LLM: ai, Model: cfg.ModelFast, HC: &http.Client{Timeout: 10 * time.Second}}
	go func() {
		err := queue.Run(ctx, db.Pool, queue.ClaimUnparsed, 10*time.Second,
			parseJob(parser, log), logUnlessShutdown(log, "parse worker"))
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("parse worker stopped", "err", err)
		}
	}()

	log.Info("listening", "addr", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}

// logUnlessShutdown keeps a normal shutdown out of the error log: on SIGTERM
// every in-flight query fails with context.Canceled, which is not a fault.
func logUnlessShutdown(log *slog.Logger, stage string) func(error) {
	return func(err error) {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error(stage, "err", err)
	}
}

func classifyJob(ai llm.Client, model string) queue.Handler {
	return func(ctx context.Context, tx pgx.Tx, j queue.Job) error {
		listID, listUnsub, precedence, err := store.Headers(ctx, tx, j.EmailID)
		if err != nil {
			return err
		}
		body, err := store.LoadBody(ctx, tx, j.EmailID)
		if err != nil {
			return err
		}
		res, err := classify.Classify(ctx, ai, model, classify.Input{
			FromEmail:  j.FromEmail,
			Subject:    j.Subject,
			HTML:       body.HTML,
			ListID:     listID,
			ListUnsub:  listUnsub,
			Precedence: precedence,
		})
		if err != nil {
			return err
		}
		return store.SetClassification(ctx, tx, j.EmailID, string(res.Class), res.NeedsConfirmation, res.Notes)
	}
}

func parseJob(p *parse.Parser, log *slog.Logger) queue.Handler {
	return func(ctx context.Context, tx pgx.Tx, j queue.Job) error {
		u, err := parse.UserFor(ctx, tx, j.UserID)
		if err != nil {
			return err
		}
		// The cheapest call is the one you skip: no LLM spend on a newsletter no
		// one will read. The row still leaves the queue.
		if !u.Billable(time.Now()) {
			log.Info("parse skipped: unbillable user", "email", j.EmailID, "user", j.UserID)
			return parse.MarkParsed(ctx, tx, j.EmailID)
		}
		senderID, err := parse.SenderID(ctx, tx, j.EmailID)
		if err != nil {
			return err
		}
		body, err := store.LoadBody(ctx, tx, j.EmailID)
		if err != nil {
			return err
		}
		raw := body.HTML
		if strings.TrimSpace(raw) == "" {
			// Plain-text newsletters still go through the same extractor.
			raw = "<pre>" + html.EscapeString(body.Text) + "</pre>"
		}
		out, err := p.Run(ctx, j.Subject, raw)
		if err != nil {
			return err
		}
		log.Info("parsed", "email", j.EmailID, "quotes", len(out.Quotes), "links", len(out.Links), "topics", out.Topics)
		return parse.Save(ctx, tx, j.EmailID, j.UserID, senderID, out)
	}
}
