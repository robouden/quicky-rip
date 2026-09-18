GO := /usr/local/go/bin/go

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

migrate:
	psql "$(MIGRATE_DATABASE_URL)" -v ON_ERROR_STOP=1 -f migrations/001_init.sql

run:
	$(GO) run ./cmd/quicky

.PHONY: build vet test migrate run
