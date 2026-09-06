SHELL := /bin/bash
COMPOSE_ARGS = $(if $(wildcard .local/dev-stack.enabled),-f compose.yaml -f compose.dev.yaml,)
REVISION := $(shell git rev-parse HEAD 2>/dev/null || echo development)
BUILD_METADATA := --build-arg REVISION=$(REVISION) --build-arg VERSION=$(REVISION)
GO_VERSION := $(shell sed -n 's/^go //p' go.mod)
.PHONY: init db dev web build test test-integration proto tools up
init:
	@test -f .env || (umask 077; go run ./cmd/providah keygen > .env)
db:
	docker compose up -d --wait db
dev: init db
	set -a; source .env; set +a; go run ./cmd/providah
web:
	cd web && npm run dev
build:
	go build ./...
	cd web && npm run build
test:
	go test ./...
test-cloud:
	python3 scripts/cloud_smoke.py
test-integration: db
	TEST_DATABASE_URL='postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable' go test -tags integration ./internal/core ./internal/database -count=1
proto:
	.bin/buf generate
	.bin/sqlc generate
tools:
	GOBIN="$(CURDIR)/.bin" go install github.com/bufbuild/buf/cmd/buf@v1.66.1
	GOBIN="$(CURDIR)/.bin" go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
	GOBIN="$(CURDIR)/.bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN="$(CURDIR)/.bin" go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0
check-tools:
	GOBIN="$(CURDIR)/.bin" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
	GOBIN="$(CURDIR)/.bin" go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0
	GOBIN="$(CURDIR)/.bin" go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
	GOBIN="$(CURDIR)/.bin" go install github.com/zricethezav/gitleaks/v8@v8.30.0
check-generated: proto
	git diff --exit-code -- internal/gen internal/database web/src/gen
	@test -z "$$(git ls-files --others --exclude-standard internal/gen internal/database web/src/gen)"
check: check-generated build
	go test -race -count=1 ./...
	.bin/golangci-lint run ./...
	python3 -m unittest discover -s scripts -p test_cloud_smoke.py
	mkdir -p .local
	.bin/gosec -quiet -exclude-generated -severity high -fmt sarif -out .local/gosec.sarif ./...
	.bin/govulncheck -format sarif ./... > .local/govulncheck.sarif
	.bin/govulncheck ./...
	cd web && npm audit --audit-level=high
	.bin/gitleaks git --redact --report-format sarif --report-path .local/gitleaks.sarif .
up: init db
	GO_VERSION=$(GO_VERSION) docker compose --profile build --profile app build
	docker compose $(COMPOSE_ARGS) --profile app up -d --no-build --no-deps --force-recreate --wait launcher app
	python3 scripts/seed_admin.py

test-browser: init db
	docker compose exec -T db dropdb -U providah --if-exists providah_browser
	docker compose exec -T db createdb -U providah providah_browser
	cd web && npm test

# Fast local build: keep Go/npm build caches on the host instead of filling Docker's VM disk.
up-fast: init db
	mkdir -p .bin/runtime/egress
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/runtime/egress-proxy ./cmd/egress-proxy
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/runtime/providah ./cmd/providah
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/runtime/provider ./cmd/provider
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/runtime/launcher ./cmd/launcher
	cd web && npm run build
	mkdir -p .bin/runtime/web
	cp -R web/dist/. .bin/runtime/web/
	docker build $(BUILD_METADATA) -f Dockerfile.runtime --target egress -t providah-egress:dev .bin/runtime
	docker build $(BUILD_METADATA) -f Dockerfile.runtime -t providah-app .bin/runtime
	docker build $(BUILD_METADATA) -f Dockerfile.runtime --target provider -t providah-provider:dev .bin/runtime
	docker build $(BUILD_METADATA) -f Dockerfile.runtime --target launcher -t providah-launcher:dev .bin/runtime
	docker compose $(COMPOSE_ARGS) --profile app up -d --no-build --no-deps --force-recreate --wait launcher app
	python3 scripts/seed_admin.py

# Run after `make up-fast`; stop the sidecar before recreating the app network namespace.
observe:
	docker compose -f compose.yaml -f compose.observability.yaml --profile app --profile observability stop observability
	docker compose -f compose.yaml -f compose.observability.yaml --profile app --profile observability up -d --no-build --wait app observability
observe-stop:
	docker compose -f compose.yaml -f compose.observability.yaml --profile app --profile observability stop observability
	docker compose --profile app up -d --no-build --no-deps --force-recreate --wait app

.PHONY: test-load
test-load: db
	TEST_DATABASE_URL='postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable' go test -tags load ./internal/core -run '^TestInventoryLoad$$' -count=1 -v

.PHONY: test-backup
test-backup: db
	PGHOST=127.0.0.1 PGPORT=55432 PGUSER=providah PGPASSWORD=providah PGDATABASE=providah TEST_BACKUP=1 python3 -m unittest discover -s scripts -p test_database_backup.py -v
	TEST_DATABASE_URL='postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable' TEST_BACKUP=1 go test -tags integration ./internal/core -run '^TestConsoleIntegration$$' -count=1 -v

.PHONY: cli
cli:
	mkdir -p .bin
	go build -trimpath -o .bin/providahctl ./cmd/providahctl

.PHONY: seed-admin
seed-admin:
	python3 scripts/seed_admin.py

# RUNTIME_IMAGE supplies a reviewed image with the CLI at /usr/local/bin.
.PHONY: automation-image
automation-image:
	@test -n "$(RUNTIME_IMAGE)" || (echo 'Set RUNTIME_IMAGE to a reviewed runtime image'; exit 1)
	mkdir -p .bin/automation-image
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/automation-image/automation-runner ./cmd/automation-runner
	docker build -f Dockerfile.automation --build-arg RUNTIME_IMAGE=$(RUNTIME_IMAGE) -t providah-automation:dev .bin/automation-image

.PHONY: dev-stack dev-stack-stop
dev-stack: init
	python3 scripts/dev_stack.py
	docker compose -f compose.yaml -f compose.dev.yaml --profile app up -d --no-build --wait
	touch .local/dev-stack.enabled
	python3 scripts/seed_admin.py
dev-stack-stop:
	rm -f .local/dev-stack.enabled
	docker compose -f compose.yaml -f compose.dev.yaml stop rustfs mailpit webhookie
	docker compose --profile app up -d --no-build --no-deps --force-recreate --wait app

.PHONY: test-dev-stack
test-dev-stack:
	TEST_RUSTFS=1 go test ./internal/auditstore ./internal/artifactstore -count=1
	python3 scripts/test_dev_capture.py

.PHONY: dev-automation dev-automation-stop
dev-automation: init
	mkdir -p .bin/automation-image
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o .bin/automation-image/automation-runner ./cmd/automation-runner
	python3 scripts/dev_automation.py
	docker compose $(COMPOSE_ARGS) --profile app up -d --no-build --no-deps --force-recreate --wait launcher app

dev-automation-stop:
	python3 scripts/dev_automation.py --stop
	docker compose $(COMPOSE_ARGS) --profile app up -d --no-build --no-deps --force-recreate --wait launcher app
