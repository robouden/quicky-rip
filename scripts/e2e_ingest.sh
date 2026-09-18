#!/usr/bin/env bash
# End-to-end check of ingest -> classify with no Anthropic call: the List-Id
# header resolves the classification on the header branch, so this exercises
# signature verification, the idempotent insert, the sender upsert, the queue
# claim and the classification write for free.
#
# Requires DATABASE_URL, MAILGUN_SIGNING_KEY, and a running server at BASE_URL.
set -euo pipefail
: "${DATABASE_URL:?set DATABASE_URL}"
: "${MAILGUN_SIGNING_KEY:?set MAILGUN_SIGNING_KEY}"
BASE_URL="${BASE_URL:-http://localhost:18080}"

ADDR="e2e@in.quicky.test"
MSGID="e2e-$(date +%s)-$RANDOM"

echo "== seeding user"
psql -qtA "$DATABASE_URL" -c \
  "INSERT INTO users (email, inbound_address) VALUES ('e2e@example.test', '$ADDR')
   ON CONFLICT (inbound_address) DO NOTHING"

TS=$(date +%s)
TOKEN="tok-$RANDOM"
SIG=$(python3 -c "import hmac,hashlib,sys;print(hmac.new(sys.argv[1].encode(),(sys.argv[2]+sys.argv[3]).encode(),hashlib.sha256).hexdigest())" \
      "$MAILGUN_SIGNING_KEY" "$TS" "$TOKEN")

post() {
  curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/webhooks/mailgun/inbound" \
    --form-string "timestamp=$TS" --form-string "token=$TOKEN" --form-string "signature=$1" \
    --form-string "recipient=$ADDR" \
    --form-string "from=Daily Thing <hello@daily.test>" \
    --form-string "subject=Issue #12: the big one" \
    --form-string "Message-Id=<$MSGID>" \
    --form-string 'body-html=<p>The FDA <a href="https://fda.test/g">guidance</a> lands Monday.</p>' \
    --form-string "message-headers=[[\"List-Id\",\"<daily.daily.test>\"],[\"List-Unsubscribe\",\"<https://daily.test/u>\"]]"
}

echo "== bad signature must be rejected"
[ "$(post deadbeef)" = "403" ] || { echo "FAIL: forged signature accepted"; exit 1; }

echo "== valid signature"
[ "$(post "$SIG")" = "200" ] || { echo "FAIL: signed webhook rejected"; exit 1; }

echo "== duplicate delivery must be a no-op that still returns 200"
[ "$(post "$SIG")" = "200" ] || { echo "FAIL: retry rejected"; exit 1; }

COUNT=$(psql -qtA "$DATABASE_URL" -c "SELECT count(*) FROM inbound_emails WHERE mailgun_message_id = '$MSGID'")
[ "$COUNT" = "1" ] || { echo "FAIL: $COUNT rows for one message id"; exit 1; }

echo "== waiting for the classify worker"
for _ in $(seq 1 20); do
  ROW=$(psql -qtA -F'|' "$DATABASE_URL" -c \
    "SELECT e.classification, e.attempts, e.classifier_notes, s.display_name, e.list_id
       FROM inbound_emails e JOIN senders s ON s.id = e.sender_id
      WHERE e.mailgun_message_id = '$MSGID'")
  case "$ROW" in
    newsletter*) break ;;
  esac
  # The classify worker polls on a 10s ticker; pace the check off the database
  # rather than spinning.
  psql -qtA "$DATABASE_URL" -c 'SELECT pg_sleep(1)' >/dev/null
done

echo "result: $ROW"
case "$ROW" in
  "newsletter|0|list-headers|Daily Thing|<daily.daily.test>")
    echo "PASS: classified on the header branch with no model call" ;;
  *) echo "FAIL: unexpected row"; exit 1 ;;
esac
