.PHONY: build run test migrate migrate-down proto-gen lint contract-replay help

# ── OS Detection ─────────────────────────────────────────────────────────────
OS := $(shell uname -s)
ifeq ($(OS),Darwin)
    INSTALL_PROTO_CMD := brew install protobuf
else
    INSTALL_PROTO_CMD := sudo apt install -y protobuf-compiler
endif

# ── Configuration ─────────────────────────────────────────────────────────────
BINARY      := bin/engine
PROTO_ROOT  := $(shell cd .. && pwd)/proto
PROTO_OUT   := api/gen/workflow/v1
OPENAPI     := api/openapi/rest-openapi.yaml
MIGRATE_DIR := migrations
DB_DSN      ?= $(WE_POSTGRES_DSN)
GOBIN       := $(shell go env GOPATH)/bin

# Ensure GOBIN is in PATH for the duration of this Makefile
export PATH := $(GOBIN):$(PATH)

help:
	@echo "Detected OS: $(OS)"
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build            Compile the engine"
	@echo "  run              Compile and run the engine"
	@echo "  test             Run unit tests"
	@echo "  migrate          Run database migrations (UP)"
	@echo "  migrate-down     Rollback all migrations"
	@echo "  proto-gen        Generate Go code from protobuf"
	@echo "  lint             Run linter (golangci-lint)"
	@echo "  contract-replay  Replay REST contract against OpenAPI"

# ── Build ──────────────────────────────────────────────────────────────────────
build:
	go build -o $(BINARY) ./cmd/engine

# ── Run ───────────────────────────────────────────────────────────────────────
run: build
	$(BINARY)

# ── Tests ─────────────────────────────────────────────────────────────────────
test:
	go test ./...

test-integration:
	go test -tags integration ./test/integration/...

# ── Database migrations ───────────────────────────────────────────────────────
migrate:
	@echo "Running migrations against $$WE_POSTGRES_DSN"
	go run ./cmd/engine -migrate-only

migrate-down:
	@echo "Rolling back all migrations"
	go run ./cmd/engine -migrate-down

# ── Proto codegen ─────────────────────────────────────────────────────────────
proto-gen:
	@command -v protoc >/dev/null 2>&1 || { \
		echo "ERROR: 'protoc' not found."; \
		echo "Suggest: $(INSTALL_PROTO_CMD)"; \
		exit 1; \
	}
	@command -v protoc-gen-go >/dev/null 2>&1 || { \
		echo "Installing protoc-gen-go..."; \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
	}
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || { \
		echo "Installing protoc-gen-go-grpc..."; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
	}
	mkdir -p $(PROTO_OUT)
	protoc \
		-I=$(PROTO_ROOT) \
		--go_out=. \
		--go_opt=module=github.com/batnam/rochallor-engine/workflow-engine \
		--go-grpc_out=. \
		--go-grpc_opt=module=github.com/batnam/rochallor-engine/workflow-engine \
		workflow/v1/engine.proto
	@echo "✅ Proto generation complete → $(PROTO_OUT)"

# ── Lint ──────────────────────────────────────────────────────────────────────
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "ERROR: golangci-lint not found."; \
		echo "Install with: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOBIN) v1.61.0"; \
		exit 1; \
	}
	golangci-lint run ./...

# ── Contract replay ───────────────────────────────────────────────────────────
contract-replay:
	@echo "Replaying REST contract against http://localhost:$$WE_REST_PORT"
	go run ./test/contract/... -openapi=$(OPENAPI) -base=http://localhost:$${WE_REST_PORT:-8080}
