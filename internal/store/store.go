// Package store is the Postgres layer. No ORM: the schema in migrations/ is the
// source of truth.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*DB, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &DB{Pool: p}, nil
}

func (d *DB) Close() { d.Pool.Close() }

var ErrNotFound = errors.New("store: not found")

// Q is the subset of pgx shared by the pool and a transaction, so stage code can
// write inside the claiming transaction.
type Q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type User struct {
	ID                 string
	TrialEndsAt        *time.Time
	SubscriptionActive bool
}

// UserByInboundAddress resolves the dedicated address (plus-tag stripped by the
// caller) to its owner.
func (d *DB) UserByInboundAddress(ctx context.Context, addr string) (User, error) {
	var u User
	err := d.Pool.QueryRow(ctx,
		`SELECT id, trial_ends_at, subscription_active FROM users WHERE inbound_address = $1`,
		addr).Scan(&u.ID, &u.TrialEndsAt, &u.SubscriptionActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// Billable reports whether the user's content is worth spending parse calls on.
// Checked before enqueueing a parse job, not inside the worker.
func (u User) Billable(now time.Time) bool {
	if u.SubscriptionActive {
		return true
	}
	return u.TrialEndsAt != nil && now.Before(*u.TrialEndsAt)
}

type InboundEmail struct {
	ID             string
	UserID         string
	SenderID       *string
	MessageID      string
	Subject        string
	FromEmail      string
	FromName       string
	Recipient      string
	PlusTag        *string
	HTML           string
	Text           string
	ListID         *string
	ListUnsub      *string
	Precedence     *string
	Classification string
}

// Body is the newsletter content, loaded by id after a claim rather than
// carried through every queue poll.
type Body struct {
	HTML string
	Text string
}

func LoadBody(ctx context.Context, q Q, emailID string) (Body, error) {
	var b Body
	err := q.QueryRow(ctx,
		`SELECT COALESCE(raw_html,''), COALESCE(raw_text,'') FROM inbound_emails WHERE id = $1`,
		emailID).Scan(&b.HTML, &b.Text)
	return b, err
}

// Headers returns the per-message list headers. They live on inbound_emails,
// not senders: a sender that once sent a List-Id must not have its later
// confirmations and one-off replies skip the classifier.
func Headers(ctx context.Context, q Q, emailID string) (listID, listUnsub, precedence string, err error) {
	var a, b, c *string
	err = q.QueryRow(ctx,
		`SELECT list_id, list_unsubscribe, precedence FROM inbound_emails WHERE id = $1`,
		emailID).Scan(&a, &b, &c)
	return str(a), str(b), str(c), err
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// InsertInbound is idempotent on mailgun_message_id: a provider retry must be a
// no-op that still lets the handler return 200.
func (d *DB) InsertInbound(ctx context.Context, e InboundEmail) (id string, inserted bool, err error) {
	err = d.Pool.QueryRow(ctx, `
		INSERT INTO inbound_emails
		  (user_id, sender_id, mailgun_message_id, subject, from_email, from_name,
		   recipient_address, plus_tag, raw_html, raw_text,
		   list_id, list_unsubscribe, precedence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (mailgun_message_id) DO NOTHING
		RETURNING id`,
		e.UserID, e.SenderID, e.MessageID, e.Subject, e.FromEmail, e.FromName,
		e.Recipient, e.PlusTag, e.HTML, e.Text,
		e.ListID, e.ListUnsub, e.Precedence).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil // already ingested
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// UpsertSender records the sender and its list headers, returning its id.
func (d *DB) UpsertSender(ctx context.Context, userID, fromEmail, displayName string, listID, listUnsub, listUnsubPost *string) (string, error) {
	var id string
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO senders (user_id, from_email, display_name, list_id, list_unsubscribe, list_unsubscribe_post)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id, from_email) DO UPDATE
		  SET display_name = COALESCE(EXCLUDED.display_name, senders.display_name),
		      list_id = COALESCE(EXCLUDED.list_id, senders.list_id),
		      list_unsubscribe = COALESCE(EXCLUDED.list_unsubscribe, senders.list_unsubscribe),
		      list_unsubscribe_post = COALESCE(EXCLUDED.list_unsubscribe_post, senders.list_unsubscribe_post),
		      updated_at = NOW()
		RETURNING id`,
		userID, fromEmail, displayName, listID, listUnsub, listUnsubPost).Scan(&id)
	return id, err
}

// EmailSummary is a row for the admin data view, not the pipeline.
type EmailSummary struct {
	ID             string
	FromEmail      string
	Subject        string
	Classification string
	ClassifierNote string
	ParsedSummary  *string
	Attempts       int
	LastError      *string
	CreatedAt      time.Time
}

// RecentEmails returns the most recently ingested emails with their
// classification and, when parsed, the parse-stage summary — for the admin
// data view.
func (d *DB) RecentEmails(ctx context.Context, limit int) ([]EmailSummary, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT e.id, e.from_email, e.subject, e.classification,
		       COALESCE(e.classifier_notes, ''), p.summary,
		       e.attempts, e.last_error, e.created_at
		  FROM inbound_emails e
		  LEFT JOIN parsed_emails p ON p.email_id = e.id
		 ORDER BY e.created_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EmailSummary
	for rows.Next() {
		var s EmailSummary
		if err := rows.Scan(&s.ID, &s.FromEmail, &s.Subject, &s.Classification,
			&s.ClassifierNote, &s.ParsedSummary, &s.Attempts, &s.LastError, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetClassification records the classifier's verdict and hands the row a fresh
// attempt budget for the parse stage. Nothing is ever deleted: non-newsletters
// land as transactional, with needs_confirmation surfaced.
func SetClassification(ctx context.Context, q Q, emailID, class string, needsConfirmation bool, notes string) error {
	_, err := q.Exec(ctx, `
		UPDATE inbound_emails
		   SET classification = $2::email_classification,
		       needs_confirmation = $3,
		       classifier_notes = NULLIF($4, ''),
		       attempts = 0,
		       last_error = NULL
		 WHERE id = $1`, emailID, class, needsConfirmation, notes)
	return err
}
