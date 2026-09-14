.PHONY: build test test-install vet fmt lint openapi-lint db-up dbtest-up db-down run-server run-runtime

build:
	go build ./...

test:
	# -p 1: integration tests share one Postgres instance, so packages must not run concurrently.
	go test -p 1 ./...

# Smoke-tests install.sh --server/--runtime against a locally built tarball
# (docs/IMPLEMENTATION_PLAN.md Slice 13). Separate from `test`: it shells
# out and writes to a temp dir rather than running as a Go test.
test-install:
	bash scripts/test-install.sh

vet:
	go vet ./...

fmt:
	gofmt -l .

lint: fmt vet

openapi-lint:
	npx -y @redocly/cli lint docs/openapi.yaml

db-up:
	docker compose up -d postgres

# Real MySQL/MSSQL test databases for internal/dbconnectors (Slice 7
# acceptance: connect to each supported engine using a real test database).
# Separate from db-up: most work doesn't need them, and MSSQL is slow to
# start.
dbtest-up:
	docker compose up -d dbtest-mysql dbtest-mssql

db-down:
	docker compose down

run-server:
	go run ./cmd/devstationone-server

run-runtime:
	go run ./cmd/devstationone-runtime
