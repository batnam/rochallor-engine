#!/usr/bin/env sh
# Entry point for the E2E integration test suite.
# Usage: ./e2e/run.sh [--sdk=go|python|node|java|all] [--transport=rest|grpc|all]
#
# Requirements: Docker + Docker Compose v2, Go 1.22+ (for the test runner).
# Run from the rochallor-engine/ directory.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "$WE_DISPATCH_MODE" = "kafka_outbox" ]; then
  COMPOSE_FILE="$SCRIPT_DIR/docker-compose-kafka-outbox.yml"
else
  COMPOSE_FILE="$SCRIPT_DIR/docker-compose-polling.yml"
fi

LOGS_DIR="$SCRIPT_DIR/logs"
RUNNER_DIR="$SCRIPT_DIR/runner"

ENGINE_REST_PORT="${ENGINE_REST_PORT:-18080}"
ENGINE_GRPC_PORT="${ENGINE_GRPC_PORT:-19090}"
SDK="all"
TRANSPORT="${TRANSPORT:-rest}"

# Parse --sdk=<value> and --transport=<value> arguments
for arg in "$@"; do
  case "$arg" in
    --sdk=*)       SDK="${arg#--sdk=}" ;;
    --transport=*) TRANSPORT="${arg#--transport=}" ;;
    --help|-h)
      echo "Usage: $0 [--sdk=go|python|node|java|all] [--transport=rest|grpc|all]"
      echo ""
      echo "Environment variables:"
      echo "  WE_DISPATCH_MODE   Dispatch mode: polling (default) or kafka_outbox"
      echo "  TRANSPORT          Client transport: rest (default), grpc, or all"
      echo "  ENGINE_REST_PORT   Host port for engine REST (default: 18080)"
      echo "  ENGINE_GRPC_PORT   Host port for engine gRPC (default: 19090)"
      echo "  POSTGRES_PORT      Host port for PostgreSQL (default: 5433)"
      exit 0
      ;;
  esac
done

if ! command -v go >/dev/null 2>&1; then
  echo "[e2e] ERROR: 'go' not found in PATH. Go 1.22+ is required to run the test runner."
  exit 1
fi

# Map --sdk flag to docker-compose profiles.
# worker-go has no profile (always starts). python/node/java use named profiles.
resolve_profiles() {
  case "$SDK" in
    go)     echo "" ;;
    python) echo "--profile python" ;;
    node)   echo "--profile node" ;;
    java)   echo "--profile java" ;;
    all)    echo "--profile python --profile node --profile java" ;;
    *)
      echo "[e2e] ERROR: unknown sdk '$SDK'; valid values: go, python, node, java, all" >&2
      exit 1
      ;;
  esac
}

PROFILES="$(resolve_profiles)"

# cleanup: collect logs then tear down stack. Always runs via trap.
cleanup() {
  echo "[e2e] collecting container logs..."
  mkdir -p "$LOGS_DIR"
  # shellcheck disable=SC2086
  docker compose -f "$COMPOSE_FILE" $PROFILES logs --no-color > "$LOGS_DIR/compose.log" 2>&1 || true
  for svc in engine worker-go worker-python worker-node worker-java; do
    # shellcheck disable=SC2086
    docker compose -f "$COMPOSE_FILE" $PROFILES logs --no-color "$svc" > "$LOGS_DIR/$svc.log" 2>&1 || true
  done
  echo "[e2e] tearing down stack..."
  # shellcheck disable=SC2086
  docker compose -f "$COMPOSE_FILE" $PROFILES down -v --remove-orphans > /dev/null 2>&1 || true
}

wait_for_engine() {
  local url="http://localhost:${ENGINE_REST_PORT}/healthz"
  local max=60
  local i=0
  echo "[e2e] waiting for engine at $url ..."
  while [ $i -lt $max ]; do
    if command -v wget >/dev/null 2>&1; then
      if wget -qO- "$url" > /dev/null 2>&1; then
        echo "[e2e] engine is healthy"
        return 0
      fi
    elif command -v curl >/dev/null 2>&1; then
      if curl -sSf "$url" > /dev/null 2>&1; then
        echo "[e2e] engine is healthy"
        return 0
      fi
    else
      echo "[e2e] ERROR: neither 'wget' nor 'curl' found in PATH. One is required for health checks."
      return 1
    fi
    sleep 1
    i=$((i + 1))
  done
  echo "[e2e] ERROR: engine did not become healthy after ${max}s"
  return 1
}

trap cleanup EXIT

echo "[e2e] building and starting stack (sdk=$SDK)..."
# shellcheck disable=SC2086
docker compose -f "$COMPOSE_FILE" $PROFILES up --build -d

wait_for_engine

echo "[e2e] running test suite (transport=$TRANSPORT)..."
cd "$RUNNER_DIR"
go run . \
  -engine="http://localhost:${ENGINE_REST_PORT}" \
  -grpc-engine="localhost:${ENGINE_GRPC_PORT}" \
  -sdk="$SDK" \
  -transport="$TRANSPORT" \
  -scenarios="$SCRIPT_DIR/scenarios"
EXIT_CODE=$?
cd - > /dev/null

exit $EXIT_CODE
