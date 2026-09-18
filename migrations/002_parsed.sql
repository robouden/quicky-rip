-- 002_parsed.sql — the parse stage's output tables.
-- Run as a role that can GRANT (not the `quicky` app role).
BEGIN;

CREATE TABLE parsed_emails (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email_id       UUID NOT NULL UNIQUE REFERENCES inbound_emails (id) ON DELETE CASCADE,
  user_id        UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  sender_id      UUID NOT NULL REFERENCES senders (id) ON DELETE CASCADE,
  summary        TEXT,
  is_onboarding  BOOLEAN NOT NULL DEFAULT FALSE,
  view_online_url TEXT,
  featured_image_src      TEXT,
  featured_image_link_url TEXT,
  topics         TEXT[] NOT NULL DEFAULT '{}',
  parsed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_parsed_emails_user_id ON parsed_emails (user_id);
CREATE INDEX idx_parsed_emails_sender_id ON parsed_emails (sender_id);
CREATE INDEX idx_parsed_emails_parsed_at ON parsed_emails (user_id, parsed_at DESC);
GRANT SELECT, INSERT, UPDATE, DELETE ON parsed_emails TO quicky;

CREATE TABLE extracted_quotes (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parsed_email_id  UUID NOT NULL REFERENCES parsed_emails (id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  quote_text       TEXT NOT NULL,
  attribution      TEXT,
  is_opinion       BOOLEAN NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_extracted_quotes_parsed_email_id ON extracted_quotes (parsed_email_id);
GRANT SELECT, INSERT, UPDATE, DELETE ON extracted_quotes TO quicky;

CREATE TABLE extracted_links (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parsed_email_id  UUID NOT NULL REFERENCES parsed_emails (id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  url              TEXT NOT NULL,
  anchor_text      TEXT,
  context          TEXT,
  is_editorial     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_extracted_links_parsed_email_id ON extracted_links (parsed_email_id);
GRANT SELECT, INSERT, UPDATE, DELETE ON extracted_links TO quicky;

CREATE TABLE time_sensitive_items (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parsed_email_id  UUID NOT NULL REFERENCES parsed_emails (id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  title            TEXT NOT NULL,
  description      TEXT,
  event_date       DATE NOT NULL,
  url              TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_time_sensitive_items_parsed_email_id ON time_sensitive_items (parsed_email_id);
CREATE INDEX idx_time_sensitive_items_event_date ON time_sensitive_items (user_id, event_date);
GRANT SELECT, INSERT, UPDATE, DELETE ON time_sensitive_items TO quicky;

COMMIT;
