-- 001_init.sql — run as a role that can GRANT (not the `quicky` app role).
-- Every CREATE TABLE is paired with its GRANT in this same transaction: a table
-- the app role cannot write to fails silently after the webhook has already 200'd.
BEGIN;

-- gen_random_uuid() is built in from PG13; pgcrypto covers older servers.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- The GRANTs below abort the whole transaction if the app role is absent.
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'quicky') THEN
    CREATE ROLE quicky LOGIN;
  END IF;
END $$;

-- NOTE: the design notes reference these enums and the `users` / `topics` tables
-- as FK targets but never define them. Defined here; adjust if the spec lands.
CREATE TYPE sender_status         AS ENUM ('active', 'paused', 'unsubscribed');
CREATE TYPE email_classification  AS ENUM ('ambiguous', 'newsletter', 'transactional');
CREATE TYPE digest_status         AS ENUM ('pending', 'generating', 'generated', 'delivered', 'failed');

CREATE TABLE users (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email             TEXT NOT NULL UNIQUE,
  inbound_address   TEXT NOT NULL UNIQUE,
  delivery_hour     SMALLINT NOT NULL DEFAULT 7 CHECK (delivery_hour BETWEEN 0 AND 23),
  timezone          TEXT NOT NULL DEFAULT 'UTC',
  trial_ends_at     TIMESTAMPTZ,
  subscription_active BOOLEAN NOT NULL DEFAULT FALSE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_users_inbound_address ON users (inbound_address);
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO quicky;

CREATE TABLE topics (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  label       TEXT NOT NULL UNIQUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON topics TO quicky;

CREATE TABLE senders (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  from_email             TEXT NOT NULL,
  display_name           TEXT,
  list_id                TEXT,
  list_unsubscribe       TEXT,
  list_unsubscribe_post  TEXT,
  status                 sender_status NOT NULL DEFAULT 'active',
  is_public              BOOLEAN NOT NULL DEFAULT FALSE,
  reputation_score       INTEGER NOT NULL DEFAULT 50 CHECK (reputation_score BETWEEN 0 AND 100),
  consecutive_negatives  INTEGER NOT NULL DEFAULT 0,
  auto_unsubscribed_at   TIMESTAMPTZ,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_senders_user_email UNIQUE (user_id, from_email)
);
CREATE INDEX idx_senders_user_id ON senders (user_id);
CREATE INDEX idx_senders_status ON senders (user_id, status);
GRANT SELECT, INSERT, UPDATE, DELETE ON senders TO quicky;

CREATE TABLE inbound_emails (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  sender_id           UUID REFERENCES senders (id) ON DELETE SET NULL,
  mailgun_message_id  TEXT NOT NULL UNIQUE,
  subject             TEXT,
  from_email          TEXT NOT NULL,
  from_name           TEXT,
  recipient_address   TEXT NOT NULL,
  plus_tag            TEXT,
  received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  storage_key         TEXT,
  raw_html            TEXT,
  raw_text            TEXT,
  -- Per-message list headers. These must NOT be read off `senders`: a sender
  -- that once sent a list header would then classify its confirmation and
  -- one-off mail as newsletters without ever reaching the classifier.
  list_id             TEXT,
  list_unsubscribe    TEXT,
  precedence          TEXT,
  needs_confirmation  BOOLEAN NOT NULL DEFAULT FALSE,
  classification      email_classification NOT NULL DEFAULT 'ambiguous',
  classifier_notes    TEXT,
  attempts            INTEGER NOT NULL DEFAULT 0,
  next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_error          TEXT,
  parsed_at           TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_inbound_emails_user_id ON inbound_emails (user_id);
CREATE INDEX idx_inbound_emails_sender_id ON inbound_emails (sender_id);
CREATE INDEX idx_inbound_emails_received_at ON inbound_emails (user_id, received_at DESC);
CREATE INDEX idx_inbound_emails_classification ON inbound_emails (classification);
-- Queue lookups: unclassified, then classified-but-unparsed.
CREATE INDEX idx_inbound_emails_unclassified ON inbound_emails (next_attempt_at)
  WHERE classification = 'ambiguous' AND attempts < 5;
CREATE INDEX idx_inbound_emails_unparsed ON inbound_emails (next_attempt_at)
  WHERE classification = 'newsletter' AND parsed_at IS NULL AND attempts < 5;
GRANT SELECT, INSERT, UPDATE, DELETE ON inbound_emails TO quicky;

COMMIT;
