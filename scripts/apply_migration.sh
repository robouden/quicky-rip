#!/usr/bin/env bash
# Applies migrations/001_init.sql to a local database named `quicky`.
# Run as a role that can CREATE ROLE and CREATE EXTENSION (postgres superuser).
# Re-runnable only after `dropdb quicky`: 001 is not idempotent by design.
set -euo pipefail
cd "$(dirname "$0")/.."

sudo -u postgres createdb quicky 2>/dev/null || echo "database quicky already exists"
# Piped, not -f: the file is read as you, not as the postgres user.
# Migrations are not idempotent by design; pass one to re-run just that file.
if [ $# -gt 0 ]; then
  files=("$@")
else
  files=(migrations/001_init.sql migrations/002_parsed.sql)
fi
for m in "${files[@]}"; do
  echo "== $m"
  sudo -u postgres psql -v ON_ERROR_STOP=1 -d quicky < "$m"
done
sudo -u postgres psql -d quicky -c "\dt"
