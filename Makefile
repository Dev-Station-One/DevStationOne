.PHONY: build test vet fmt lint openapi-lint db-up db-down run-server run-runtime

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

lint: fmt vet

openapi-lint:
	npx -y @redocly/cli lint docs/openapi.yaml

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

run-server:
	go run ./cmd/devstation-server

run-runtime:
	go run ./cmd/devstation-runtime
