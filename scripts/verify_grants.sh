#!/usr/bin/env bash
# Proves the app role can actually write every table, by doing the writes AS
# that role and rolling them back. A missing GRANT here is the failure mode that
# otherwise shows up as a webhook returning 200 while the email is lost.
#
# Connects as the app role itself via DATABASE_URL, so it tests the real grant
# path rather than a superuser impersonating it.
set -euo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the app role connection string}"

psql -v ON_ERROR_STOP=1 "$DATABASE_URL" <<'SQL'
BEGIN;

INSERT INTO users (email, inbound_address)
  VALUES ('grant-check@example.test', 'grant-check@in.quicky.test');

INSERT INTO senders (user_id, from_email, display_name)
  SELECT id, 'sender@example.test', 'Example Sender' FROM users
   WHERE email = 'grant-check@example.test';

INSERT INTO inbound_emails
  (user_id, sender_id, mailgun_message_id, subject, from_email, recipient_address,
   raw_html, list_id)
  SELECT u.id, s.id, 'grant-check-1', 'Issue #1', 'sender@example.test',
         'grant-check@in.quicky.test', '<p>hi</p>', '<list.example.test>'
    FROM users u JOIN senders s ON s.user_id = u.id
   WHERE u.email = 'grant-check@example.test';

INSERT INTO topics (label) VALUES ('grant check topic');

-- The idempotency key must swallow a duplicate rather than error.
INSERT INTO inbound_emails
  (user_id, sender_id, mailgun_message_id, from_email, recipient_address)
  SELECT user_id, sender_id, 'grant-check-1', from_email, recipient_address
    FROM inbound_emails WHERE mailgun_message_id = 'grant-check-1'
  ON CONFLICT (mailgun_message_id) DO NOTHING;

SELECT classification, attempts, next_attempt_at <= NOW() AS claimable
  FROM inbound_emails WHERE mailgun_message_id = 'grant-check-1';

-- Parse-stage tables (002). Shapes mirror parse.Save exactly: TEXT[] topics,
-- nullable featured image, ISO date cast, NULLIF on optional strings.
INSERT INTO parsed_emails
  (email_id, user_id, sender_id, summary, is_onboarding, view_online_url,
   featured_image_src, featured_image_link_url, topics)
  SELECT e.id, e.user_id, e.sender_id, 'A summary.', FALSE, NULL,
         'https://img.test/p.jpg', NULL, ARRAY['topic a','topic b','topic c']
    FROM inbound_emails e WHERE e.mailgun_message_id = 'grant-check-1';

-- Idempotency on email_id must be a swallowed no-op, not an error.
INSERT INTO parsed_emails (email_id, user_id, sender_id, topics)
  SELECT e.id, e.user_id, e.sender_id, '{}' FROM inbound_emails e
   WHERE e.mailgun_message_id = 'grant-check-1'
  ON CONFLICT (email_id) DO NOTHING;

INSERT INTO extracted_quotes (parsed_email_id, user_id, quote_text, attribution, is_opinion)
  SELECT p.id, p.user_id, 'A striking claim.', NULLIF('', ''), TRUE FROM parsed_emails p;

INSERT INTO extracted_links (parsed_email_id, user_id, url, anchor_text, context, is_editorial)
  SELECT p.id, p.user_id, 'https://fda.test/g', 'guidance', 'why it matters', TRUE FROM parsed_emails p;

INSERT INTO time_sensitive_items (parsed_email_id, user_id, title, description, event_date, url)
  SELECT p.id, p.user_id, 'Register for the thing', NULLIF('', ''), '2026-09-25'::date, NULLIF('', '')
    FROM parsed_emails p;

SELECT (SELECT count(*) FROM parsed_emails)        AS parsed,
       (SELECT count(*) FROM extracted_quotes)     AS quotes,
       (SELECT count(*) FROM extracted_links)      AS links,
       (SELECT count(*) FROM time_sensitive_items) AS items;

ROLLBACK;
SQL
echo "OK: app role can write every table; nothing was left behind."
