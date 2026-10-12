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

IMAGE_OVERRIDE=""
compose() {
  # shellcheck disable=SC2086
  if [ -n "$IMAGE_OVERRIDE" ]; then
    docker compose -f "$COMPOSE_FILE" -f "$IMAGE_OVERRIDE" $PROFILES "$@"
  else
    docker compose -f "$COMPOSE_FILE" $PROFILES "$@"
  fi
}

LOGS_DIR="${E2E_LOGS_DIR:-$SCRIPT_DIR/logs}"
mkdir -p "$LOGS_DIR"
LOGS_DIR="$(cd "$LOGS_DIR" && pwd)"
export E2E_AUDIT_LOG_DIR="$LOGS_DIR"
RUNNER_DIR="$SCRIPT_DIR/runner"

ENGINE_REST_PORT="${ENGINE_REST_PORT:-18080}"
ENGINE_GRPC_PORT="${ENGINE_GRPC_PORT:-19090}"
export E2E_ENGINE_HEALTH_PATH="${E2E_ENGINE_HEALTH_PATH:-readyz}"
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
      echo "  E2E_ENGINE_IMAGE   Use a frozen engine image instead of building"
      echo "  E2E_WORKER_IMAGE   Use a frozen worker image (--sdk must name one SDK)"
      echo "  E2E_ENGINE_HEALTH_PATH  Health endpoint (default: readyz; healthz for older engines)"
      echo "  E2E_LOGS_DIR       Directory for this run logs and image identities"
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
# python/node/java use named profiles; explicit service selection isolates one SDK.
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

if [ -n "$E2E_WORKER_IMAGE" ] && [ "$SDK" = "all" ]; then
  echo "[e2e] ERROR: E2E_WORKER_IMAGE requires one --sdk" >&2
  exit 1
fi

if [ -z "$E2E_WORKER_IMAGE" ]; then
# Node E2E consumes a previously verified tarball, including on release tags.
# Never rebuild here: all transport/dispatch combinations must test the same bytes.
case "$SDK" in
  node|all)
    if [ ! -f "$SCRIPT_DIR/../workflow-sdk-node/artifacts/sdk.tgz" ] ||
       [ ! -f "$SCRIPT_DIR/../workflow-sdk-node/artifacts/SHA256SUMS" ]; then
      echo "[e2e] ERROR: prepare the Node candidate: cd workflow-sdk-node && npm ci && npm run package:verify" >&2
      exit 1
    fi
    ;;
esac

case "$SDK" in
  java|all)
    if [ ! -f "$SCRIPT_DIR/../workflow-sdk-java/artifacts/candidate/SHA256SUMS" ]; then
      echo "[e2e] ERROR: prepare Java candidate: cd workflow-sdk-java && python3 scripts/verify-package.py" >&2
      exit 1
    fi
    ;;
esac

case "$SDK" in
  python|all)
    if [ ! -f "$SCRIPT_DIR/../workflow-sdk-python/dist/SHA256SUMS" ]; then
      echo "[e2e] ERROR: prepare Python candidate: cd workflow-sdk-python && python scripts/verify-package.py" >&2
      exit 1
    fi
    ;;
esac

fi

# cleanup: collect logs then tear down stack. Always runs via trap.
cleanup() {
  echo "[e2e] collecting container logs..."
  mkdir -p "$LOGS_DIR"
  # shellcheck disable=SC2086
  compose logs --no-color > "$LOGS_DIR/compose.log" 2>&1 || true
  for svc in engine worker-go worker-python worker-node worker-java fault-proxy; do
    service="$svc"
    if [ "$WE_DISPATCH_MODE" = "kafka_outbox" ] && [ "$svc" != "fault-proxy" ]; then service="e2e-$svc"; fi
    # shellcheck disable=SC2086
    compose logs --no-color "$service" > "$LOGS_DIR/$svc.log" 2>&1 || true
  done
  containers="$(compose ps -aq)"
  if [ -n "$containers" ]; then
    # shellcheck disable=SC2086
    docker inspect --format '{{.Name}} {{.Image}}' $containers > "$LOGS_DIR/images.txt" || true
  fi
  echo "[e2e] tearing down stack..."
  # shellcheck disable=SC2086
  compose down -v --remove-orphans > /dev/null 2>&1 || true
  if [ -n "$IMAGE_OVERRIDE" ]; then rm -f "$IMAGE_OVERRIDE"; fi
}

wait_for_engine() {
  local url="http://localhost:${ENGINE_REST_PORT}/${E2E_ENGINE_HEALTH_PATH}"
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

trap 'STATUS=$?; trap - EXIT; cleanup; exit "$STATUS"' EXIT

if [ -n "$E2E_ENGINE_IMAGE" ] || [ -n "$E2E_WORKER_IMAGE" ]; then
  IMAGE_OVERRIDE="$(mktemp)"
  echo "services:" > "$IMAGE_OVERRIDE"
  if [ -n "$E2E_ENGINE_IMAGE" ]; then
    ENGINE_SERVICE=engine
    if [ "$WE_DISPATCH_MODE" = "kafka_outbox" ]; then ENGINE_SERVICE=e2e-engine; fi
    export E2E_ENGINE_IMAGE
    cat >> "$IMAGE_OVERRIDE" <<EOF
  $ENGINE_SERVICE:
    build: !reset null
    image: \${E2E_ENGINE_IMAGE}
EOF
  fi
  if [ -n "$E2E_WORKER_IMAGE" ]; then
    WORKER_SERVICE="worker-$SDK"
    if [ "$WE_DISPATCH_MODE" = "kafka_outbox" ]; then WORKER_SERVICE="e2e-$WORKER_SERVICE"; fi
    export E2E_WORKER_IMAGE
    cat >> "$IMAGE_OVERRIDE" <<EOF
  $WORKER_SERVICE:
    build: !reset null
    image: \${E2E_WORKER_IMAGE}
EOF
  fi
fi

echo "[e2e] building and starting stack (sdk=$SDK, transport=$TRANSPORT)..."
export WORKER_TRANSPORT="$TRANSPORT"
WORKER_SERVICE=""
if [ "$SDK" != "all" ]; then
  WORKER_SERVICE="worker-$SDK"
  if [ "$WE_DISPATCH_MODE" = "kafka_outbox" ]; then WORKER_SERVICE="e2e-$WORKER_SERVICE"; fi
fi
# shellcheck disable=SC2086
compose up --build -d $WORKER_SERVICE

wait_for_engine

echo "[e2e] running test suite (transport=$TRANSPORT)..."
cd "$RUNNER_DIR"
E2E_TIMER_INTERRUPTING=1 go run . \
  -engine="http://localhost:${ENGINE_REST_PORT}" \
  -grpc-engine="localhost:${ENGINE_GRPC_PORT}" \
  -sdk="$SDK" \
  -transport="$TRANSPORT" \
  -scenarios="$SCRIPT_DIR/scenarios"
EXIT_CODE=$?
cd - > /dev/null

exit $EXIT_CODE
