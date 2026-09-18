// Package queue drives pipeline stages off the durable stage markers already in
// the schema (classification, parsed_at) with SELECT ... FOR UPDATE SKIP LOCKED.
// No broker: the database is the queue.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxAttempts bounds retries so a poison row (bad body, expired API key) stops
// spinning instead of being re-claimed forever. The budget is per stage: each
// stage transition resets `attempts`, so a row that needed three tries to
// classify still gets a full budget to parse.
const MaxAttempts = 5

// Job is one claimed email. It carries identifiers only — bodies are routinely
// hundreds of KB and are loaded by the handler after the claim, never dragged
// through every poll.
type Job struct {
	EmailID   string
	UserID    string
	FromEmail string
	Subject   string
	Attempts  int
}

// Handler runs in its own transaction, after the claim has been committed. Its
// writes commit or roll back together; on rollback the row is retried once its
// backoff elapses.
type Handler func(ctx context.Context, tx pgx.Tx, j Job) error

const claimCols = `id, user_id, from_email, COALESCE(subject,''), attempts`

// ClaimUnclassified claims one email awaiting classification.
const ClaimUnclassified = `
	SELECT ` + claimCols + `
	  FROM inbound_emails
	 WHERE classification = 'ambiguous'
	   AND attempts < ` + maxAttemptsSQL + `
	   AND next_attempt_at <= NOW()
	 ORDER BY next_attempt_at
	 FOR UPDATE SKIP LOCKED
	 LIMIT 1`

// ClaimUnparsed claims one classified newsletter awaiting parse.
const ClaimUnparsed = `
	SELECT ` + claimCols + `
	  FROM inbound_emails
	 WHERE classification = 'newsletter' AND parsed_at IS NULL
	   AND attempts < ` + maxAttemptsSQL + `
	   AND next_attempt_at <= NOW()
	 ORDER BY next_attempt_at
	 FOR UPDATE SKIP LOCKED
	 LIMIT 1`

const maxAttemptsSQL = "5"

// Run polls claimSQL and runs h inside the claiming transaction, so a crashed
// worker releases the row rather than losing the email. Handler errors are
// reported to onErr; the row backs off and is retried up to MaxAttempts.
func Run(ctx context.Context, pool *pgxpool.Pool, claimSQL string, interval time.Duration, h Handler, onErr func(error)) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		for {
			worked, err := step(ctx, pool, claimSQL, h)
			if err != nil {
				onErr(err)
				break
			}
			if !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func step(ctx context.Context, pool *pgxpool.Pool, claimSQL string, h Handler) (bool, error) {
	// Claim and charge the attempt in one short committed transaction. The
	// handler runs afterwards, on its own transaction: bumping the counter from
	// a second connection while this one still held the row lock would deadlock
	// against itself, and an attempt that is rolled back never backs off.
	j, ok, err := claim(ctx, pool, claimSQL)
	if err != nil || !ok {
		return false, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := h(ctx, tx, j); err != nil {
		recordFailure(ctx, pool, j, err)
		return false, err
	}

	return true, tx.Commit(ctx)
}

func claim(ctx context.Context, pool *pgxpool.Pool, claimSQL string) (Job, bool, error) {
	var j Job
	tx, err := pool.Begin(ctx)
	if err != nil {
		return j, false, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, claimSQL).Scan(&j.EmailID, &j.UserID, &j.FromEmail, &j.Subject, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	// Exponential backoff off the pre-increment attempt count: 1m, 3m, 9m, 27m.
	if _, err := tx.Exec(ctx, `
		UPDATE inbound_emails
		   SET attempts = attempts + 1,
		       next_attempt_at = NOW() + (INTERVAL '1 minute' * POWER(3, attempts))
		 WHERE id = $1`, j.EmailID); err != nil {
		return j, false, err
	}
	return j, true, tx.Commit(ctx)
}

// recordFailure is best-effort: the reason a row stopped progressing should be
// visible on the row itself, not only in logs. It detaches from ctx — the
// common failure is a cancelled context, and writing the reason on a dead
// context would silently drop it exactly when it matters.
func recordFailure(ctx context.Context, pool *pgxpool.Pool, j Job, cause error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pool.Exec(ctx, `UPDATE inbound_emails SET last_error = $2 WHERE id = $1`, j.EmailID, cause.Error())
}

// ResetAttempts hands a row a fresh budget at a stage transition. Every stage
// that advances a row must call it, or the next stage inherits a spent budget.
func ResetAttempts(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, emailID string) error {
	_, err := q.Exec(ctx,
		`UPDATE inbound_emails SET attempts = 0, next_attempt_at = NOW(), last_error = NULL WHERE id = $1`,
		emailID)
	return err
}
