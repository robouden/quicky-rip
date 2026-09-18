package parse

import (
	"context"
	"errors"

	"codeberg.org/Safecast/quicky/internal/store"
	"github.com/jackc/pgx/v5"
)

// Save writes the extraction and all its children, then marks the email parsed.
// It runs inside the caller's transaction: a half-written parse is worse than
// an unparsed email, since the coverage check downstream would count the sender
// as present with nothing behind it.
//
// It is idempotent on email_id: if a previous attempt's commit was lost after
// the insert landed, the re-claimed row just gets marked parsed instead of
// failing on the unique constraint for every remaining attempt.
func Save(ctx context.Context, tx pgx.Tx, emailID, userID, senderID string, p ParsedNewsletter) error {
	var src, linkURL *string
	if p.FeaturedImage != nil && p.FeaturedImage.Src != "" {
		src = &p.FeaturedImage.Src
		linkURL = p.FeaturedImage.LinkURL
	}

	var parsedID string
	err := tx.QueryRow(ctx, `
		INSERT INTO parsed_emails
		  (email_id, user_id, sender_id, summary, is_onboarding, view_online_url,
		   featured_image_src, featured_image_link_url, topics)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (email_id) DO NOTHING
		RETURNING id`,
		emailID, userID, senderID, p.Summary, p.IsOnboarding, p.ViewOnlineURL,
		src, linkURL, p.Topics).Scan(&parsedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MarkParsed(ctx, tx, emailID)
	}
	if err != nil {
		return err
	}

	for _, q := range p.Quotes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO extracted_quotes (parsed_email_id, user_id, quote_text, attribution, is_opinion)
			VALUES ($1,$2,$3,NULLIF($4,''),$5)`,
			parsedID, userID, q.Text, q.Attribution, q.IsOpinion); err != nil {
			return err
		}
	}
	for _, l := range p.Links {
		if _, err := tx.Exec(ctx, `
			INSERT INTO extracted_links (parsed_email_id, user_id, url, anchor_text, context, is_editorial)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			parsedID, userID, l.URL, l.AnchorText, l.Context, l.IsEditorial); err != nil {
			return err
		}
	}
	for _, it := range p.TimeSensitiveItems {
		if _, err := tx.Exec(ctx, `
			INSERT INTO time_sensitive_items (parsed_email_id, user_id, title, description, event_date, url)
			VALUES ($1,$2,$3,NULLIF($4,''),$5::date,NULLIF($6,''))`,
			parsedID, userID, it.Title, it.Description, it.EventDate, it.URL); err != nil {
			return err
		}
	}

	return MarkParsed(ctx, tx, emailID)
}

// MarkParsed closes the stage. It is also the right call for an email that is
// deliberately not parsed (an unbillable user): the row must leave the queue
// either way, or it is re-claimed until its attempt budget runs out.
func MarkParsed(ctx context.Context, tx pgx.Tx, emailID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE inbound_emails SET parsed_at = NOW(), attempts = 0, last_error = NULL WHERE id = $1`,
		emailID)
	return err
}

// SenderID is required by parsed_emails and is not carried on the queue job.
func SenderID(ctx context.Context, q store.Q, emailID string) (string, error) {
	var id *string
	if err := q.QueryRow(ctx,
		`SELECT sender_id FROM inbound_emails WHERE id = $1`, emailID).Scan(&id); err != nil {
		return "", err
	}
	if id == nil {
		return "", errors.New("parse: email has no sender")
	}
	return *id, nil
}

// UserFor loads the billing state that decides whether this email is parsed at
// all. The cheapest call is the one you skip.
func UserFor(ctx context.Context, q store.Q, userID string) (store.User, error) {
	var u store.User
	err := q.QueryRow(ctx,
		`SELECT id, trial_ends_at, subscription_active FROM users WHERE id = $1`,
		userID).Scan(&u.ID, &u.TrialEndsAt, &u.SubscriptionActive)
	return u, err
}
