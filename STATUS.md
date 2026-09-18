# Go build status

Spine only. `go build ./...`, `go vet ./...`, `go test ./...` pass.

`migrations/001_init.sql` is applied to the local `quicky` database, and the app role's
write access is verified by `scripts/verify_grants.sh` (inserts as `quicky`, then rolls
back). The binary starts against it, serves `/healthz`, and rejects an unsigned webhook
with 403.

## Done
- `migrations/001_init.sql` — enums, `users`, `topics`, `senders`, `inbound_emails`; every
  CREATE TABLE paired with its GRANT in one transaction.
- `internal/ingest` — Mailgun webhook: HMAC verify, idempotent insert, 200.
- `internal/queue` — DB-as-queue, `FOR UPDATE SKIP LOCKED`, attempt budget + backoff.
- `internal/classify` — full decision order, LLM behind an interface.
- `internal/llm` — Messages client with `cache_control: ephemeral` and forced tool use.
- `internal/prompts` — prompts embedded verbatim from README.md as the spec.
- `internal/htmlx` — HTML to cleaned text (anchors inline as `[text](url)`), image
  candidates in document order, tracking-redirect resolution that fails open.

- `internal/parse` — extraction stage, unit-tested against a stubbed LLM: cached system
  prompt, forced EXTRACT_TOOL, per-quote cold-reader gate (transport errors retry the
  parse rather than dropping the quote), editorial-only link canonicalization through a
  pool of 12, idempotent `Save`, unbillable users skipped before any model call.
  `migrations/002_parsed.sql` is applied. **Live-verified on Haiku** against a realistic
  fixture: tracking wrapper unwrapped, logo rejected in favour of the editorial photo,
  footer/CTA links excluded, one author-voice quote through the cold-reader gate, and
  the skip path confirmed to make zero calls for an unbillable user.

## Stubs
`internal/synth`, `internal/deliver` (only the backoff table is real).

## For synth
- `parsed_emails.is_onboarding` is stored but nothing consumes it yet. Welcome and
  confirmation emails should not reach the digest.

## Deviations from the design notes
- `inbound_emails` stores `raw_html`/`raw_text` inline instead of `storage_key` +
  object storage. Bodies are loaded by id after a claim, never in the claim SELECT.
- Enums and the `users` / `topics` tables are invented here — the notes reference them
  as FK targets but never define them.
- `list_id` / `list_unsubscribe` / `precedence` live on `inbound_emails`, not `senders`.
- The parser user prompt starts with `Today's date: YYYY-MM-DD`, which the documented
  template lacks. Without it the time-sensitive rule is unanswerable; the first live run
  returned a date a year off.

`scripts/e2e_ingest.sh` passes against the local database: forged signature rejected,
signed delivery accepted, duplicate delivery a 200 no-op leaving one row, and the
classify worker resolving it to `newsletter` on the `list-headers` branch with no model
call. It leaves its seeded user and one classified email behind as fixture data for the
parse stage.

## Known, not yet fixed
- Credentials live in `.env` (gitignored); launch with `set -a; . ./.env; set +a`.
- The classifier's LLM fallback branch has not been exercised live — every fixture so
  far carried list headers.
- The classify handler holds a Postgres transaction across a 120s Anthropic call.
  Fine at one worker, pool-exhausting at ten.
- Unverified: whether Mailgun's inbound parse surfaces `List-Id` as an individual form
  field. If it only arrives inside `message-headers`, `headers()` already covers it —
  but if neither is present, every email falls through to the paid classifier and the
  "resolve most without a model call" cost design is gone.
