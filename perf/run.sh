#!/usr/bin/env sh
# Performance load test entry point for the rochallor-engine.
# See specs/003-performance-load-testing/quickstart.md for full usage.
# Run from the repository root: ./perf/run.sh
#
# Phase 2: skeleton + surface-drift canary (T007/T008) + k6 version pin (T009)
# Phase 3: adds k6 scenario + exit-code wiring + minimum report (T010-T016)
# Phase 4: adds full report, host stats, engine metrics snapshot (T018-T025)
# Phase 5: adds --compare-to regression detection (T027-T031)
# Phase 6: adds --smoke, --containerized-k6, --engine-image, --note (T033-T040)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"

# --- Pinned tool versions (T009) ---
PERF_K6_VERSION_PIN="1.7"   # expects k6 v0.50.x on the host

# --- Default run configuration ---
PERF_ENGINE_REST_PORT="${PERF_ENGINE_REST_PORT:-18080}"
PERF_ENGINE_METRICS_PORT="${PERF_ENGINE_METRICS_PORT:-19091}"
PERF_RAMP_UP="2m"
PERF_STEADY="10m"
PERF_RAMP_DOWN="1m"
PERF_TARGET_VUS="1000"
PERF_NOTE=""
PERF_COMPARE_TO=""
PERF_COMPARE_TO_LATEST=false
PERF_SMOKE=false
PERF_CONTAINERIZED_K6=false
PERF_ENGINE_IMAGE=""
PERF_THRESHOLD_OVERRIDES=""
PERF_GRPC=false
PERF_WORKER_TRANSPORT=rest
PERF_DISPATCH_MODE=polling
PERF_SCENARIO="thousand-concurrent"

# --- Flag parsing ---
while [ $# -gt 0 ]; do
  case "$1" in
    --ramp-up=*)               PERF_RAMP_UP="${1#--ramp-up=}" ;;
    --steady=*)                PERF_STEADY="${1#--steady=}" ;;
    --ramp-down=*)             PERF_RAMP_DOWN="${1#--ramp-down=}" ;;
    --target-vus=*)            PERF_TARGET_VUS="${1#--target-vus=}" ;;
    --note=*)                  PERF_NOTE="${1#--note=}" ;;
    --note)                    shift; PERF_NOTE="$1" ;;
    --compare-to=*)            PERF_COMPARE_TO="${1#--compare-to=}" ;;
    --compare-to-latest)       PERF_COMPARE_TO_LATEST=true ;;
    --smoke)                   PERF_SMOKE=true ;;
    --containerized-k6)        PERF_CONTAINERIZED_K6=true ;;
    --engine-image=*)          PERF_ENGINE_IMAGE="${1#--engine-image=}" ;;
    --threshold-overrides=*)   PERF_THRESHOLD_OVERRIDES="${1#--threshold-overrides=}" ;;
    --grpc)                    PERF_GRPC=true; PERF_WORKER_TRANSPORT=grpc ;;
    --kafka)                   PERF_DISPATCH_MODE=kafka_outbox ;;
    --scenario=*)              PERF_SCENARIO="${1#--scenario=}" ;;
    --scenario)                shift; PERF_SCENARIO="$1" ;;
    --help|-h)
      echo "Usage: $0 [options]"
      echo ""
      echo "Options:"
      echo "  --grpc                     Run k6 scenario and workers over gRPC (default: REST)"
      echo "  --kafka                    Run engine + workers in kafka_outbox dispatch mode"
      echo "  --ramp-up=<d>              k6 ramp-up duration (default: 2m)"
      echo "  --steady=<d>               k6 steady-state duration (default: 10m)"
      echo "  --ramp-down=<d>            k6 ramp-down duration (default: 1m)"
      echo "  --target-vus=<n>           Concurrent VUs at peak (default: 1000)"
      echo "  --smoke                    50-VU, 30-second harness-validation run"
      echo "  --compare-to=<runId>       Compare against a prior run (Phase 5)"
      echo "  --compare-to-latest        Compare against most recent passing run (Phase 5)"
      echo "  --note=<text>              Attach an engineer note to the run report (Phase 6)"
      echo "  --containerized-k6         Run k6 inside a container (Phase 6)"
      echo "  --engine-image=<ref>       Use a pre-built engine image (Phase 6)"
      echo "  --threshold-overrides=<f>  Override strict thresholds from a file (Phase 6)"
      echo "  --scenario=<name>          Run a specific scenario (default: thousand-concurrent)"
      echo "                             Options: thousand-concurrent, loan-chain-full"
      echo ""
      echo "Environment variables:"
      echo "  PERF_ENGINE_REST_PORT      Host port for engine REST (default: 18080)"
      echo "  PERF_ENGINE_GRPC_PORT      Host port for engine gRPC (default: 19090)"
      echo "  PERF_ENGINE_METRICS_PORT   Host port for engine /metrics (default: 19091)"
      exit 0
      ;;
    *)
      echo "[perf] ERROR: unknown flag '$1' — use --help for usage." >&2
      exit 1
      ;;
  esac
  shift
done


# Apply smoke-mode overrides
if [ "$PERF_SMOKE" = "true" ]; then
  PERF_RAMP_UP="10s"
  PERF_STEADY="30s"
  PERF_RAMP_DOWN="10s"
  PERF_TARGET_VUS="50"
  echo "[perf] smoke mode: ${PERF_TARGET_VUS} VUs, ${PERF_STEADY} steady"
fi

if [ "$PERF_GRPC" = "true" ]; then
  PERF_ENGINE_GRPC_URL="localhost:${PERF_ENGINE_GRPC_PORT:-19090}"
  PERF_PROTO_ROOT="${REPO_ROOT}/proto"
  export PERF_ENGINE_GRPC_URL PERF_PROTO_ROOT
fi

PERF_ENGINE_BASE_URL="http://localhost:${PERF_ENGINE_REST_PORT}"

# --- Run-id generation ---
ENGINE_SHA="$(cd "$REPO_ROOT/workflow-engine" && git rev-parse --short=7 HEAD 2>/dev/null || echo "unknown")"
RUN_ID="$(date -u '+%Y%m%d-%H%M%S')-${ENGINE_SHA}"
REPORTS_DIR="$SCRIPT_DIR/reports/$RUN_ID"
mkdir -p "$REPORTS_DIR"

export PERF_RUN_ID="$RUN_ID"
export PERF_REPORTS_DIR="$REPORTS_DIR"
export PERF_ENGINE_BASE_URL
export PERF_RAMP_UP PERF_STEADY PERF_RAMP_DOWN PERF_TARGET_VUS
export PERF_NOTE PERF_COMPARE_TO PERF_COMPARE_TO_LATEST
export PERF_THRESHOLD_OVERRIDES
export PERF_WORKER_TRANSPORT
export PERF_DISPATCH_MODE

echo "[perf] runId=${RUN_ID}"
echo "[perf] engine build=${ENGINE_SHA} (workflow-engine)"
echo "[perf] target=${PERF_TARGET_VUS} VUs | ramp-up=${PERF_RAMP_UP} | steady=${PERF_STEADY} | ramp-down=${PERF_RAMP_DOWN}"

# --- k6 version pin check (T009) ---
if [ "$PERF_CONTAINERIZED_K6" = "true" ]; then
  echo "[perf] containerized-k6 mode: skipping host k6 version check"
else
  if ! command -v k6 >/dev/null 2>&1; then
    echo "[perf] ERROR: k6 not found in PATH." >&2
    echo "[perf]   Install k6 v${PERF_K6_VERSION_PIN}.x or run with --containerized-k6." >&2
    exit 1
  fi
  K6_VER_LINE="$(k6 version 2>&1 | head -1)"
  if ! echo "$K6_VER_LINE" | grep -qF "v${PERF_K6_VERSION_PIN}"; then
    echo "[perf] ERROR: k6 version mismatch." >&2
    echo "[perf]   expected: v${PERF_K6_VERSION_PIN}.x" >&2
    echo "[perf]   found:    ${K6_VER_LINE}" >&2
    echo "[perf]   Install the pinned version or use --containerized-k6." >&2
    exit 1
  fi
  echo "[perf] k6 ${K6_VER_LINE} — OK"
fi

# --- Pre-flight: Docker + Compose availability ---
if ! command -v docker >/dev/null 2>&1; then
  echo "[perf] ERROR: docker not found in PATH." >&2; exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "[perf] ERROR: docker compose v2 not found." >&2
  echo "[perf]   Ensure Docker Desktop >= 3.3 or install the docker-compose-plugin." >&2
  exit 1
fi

# Warn (non-fatal) if key ports look occupied
for port in "${PERF_ENGINE_REST_PORT}" "${PERF_ENGINE_METRICS_PORT}"; do
  if command -v ss >/dev/null 2>&1; then
    if ss -ltn 2>/dev/null | grep -q ":${port} "; then
      echo "[perf] WARNING: port ${port} appears to be in use — compose may fail."
    fi
  fi
done
if [ "$PERF_GRPC" = "true" ] && command -v ss >/dev/null 2>&1; then
  if ss -ltn 2>/dev/null | grep -q ":${PERF_ENGINE_GRPC_PORT:-19090} "; then
    echo "[perf] WARNING: port ${PERF_ENGINE_GRPC_PORT:-19090} appears to be in use — compose may fail."
  fi
fi

# --- Cleanup: always tear down the stack (runs on EXIT) ---
cleanup() {
  EXIT_CODE=$?
  # Kill k6 / host-stats if still running (e.g., interrupted mid-run)
  [ -n "$K6_PROC_PID" ]    && kill "$K6_PROC_PID"    2>/dev/null || true
  [ -n "$HOST_STATS_PID" ] && kill "$HOST_STATS_PID" 2>/dev/null || true
  echo "[perf] tearing down compose stack..."
  PERF_ENGINE_IMAGE="${PERF_ENGINE_IMAGE:-}" \
    docker compose ${COMPOSE_ARGS:--f "$COMPOSE_FILE"} down -v --remove-orphans > /dev/null 2>&1 || true
  exit "${PERF_K6_EXIT:-$EXIT_CODE}"
}
trap cleanup EXIT

# --- Boot the compose stack ---
# T036: if --engine-image was provided, use the override file that replaces the build.
COMPOSE_ARGS="-f $COMPOSE_FILE"
if [ -n "$PERF_ENGINE_IMAGE" ]; then
  COMPOSE_ARGS="$COMPOSE_ARGS -f $SCRIPT_DIR/docker-compose.image.yml"
  echo "[perf] using engine image: $PERF_ENGINE_IMAGE"
fi
if [ "$PERF_DISPATCH_MODE" = "kafka_outbox" ]; then
  COMPOSE_ARGS="$COMPOSE_ARGS -f $SCRIPT_DIR/docker-compose.kafka.yml"
  echo "[perf] dispatch mode: kafka_outbox (Kafka overlay active)"
fi
echo "[perf] booting compose stack..."
PERF_ENGINE_IMAGE="$PERF_ENGINE_IMAGE" \
  docker compose $COMPOSE_ARGS up --build -d

# --- Wait for engine health (≤60 s) ---
wait_for_engine() {
  local url="${PERF_ENGINE_BASE_URL}/healthz"
  local max=60 i=0
  echo "[perf] waiting for engine at ${url} ..."
  while [ "$i" -lt "$max" ]; do
    if command -v wget >/dev/null 2>&1; then
      if wget -qO- "$url" > /dev/null 2>&1; then
        echo "[perf] engine healthy after ${i}s"; return 0
      fi
    elif command -v curl >/dev/null 2>&1; then
      if curl -sSf "$url" > /dev/null 2>&1; then
        echo "[perf] engine healthy after ${i}s"; return 0
      fi
    else
      echo "[perf] ERROR: neither wget nor curl found." >&2; exit 1
    fi
    sleep 1; i=$((i + 1))
  done
  echo "[perf] ERROR: engine did not become healthy after ${max}s." >&2
  exit 1
}
wait_for_engine

# --- Surface-drift canary (T008) ---
# Verifies the engine REST surface still matches the contract before VUs ramp.
# Aborts (surface_drift) if the engine returns unexpected shapes.
# Contract: specs/003-performance-load-testing/contracts/engine-endpoints.md
surface_drift_check() {
  echo "[perf] surface drift check..."

  # Minimal HTTP helpers (curl required for POST with body)
  _post() {
    local url="$1" body="$2"
    if command -v curl >/dev/null 2>&1; then
      curl -sf -X POST \
        -H "Content-Type: application/json" \
        -H "Accept: application/json" \
        -d "$body" \
        "$url"
    else
      echo "[perf] ERROR: curl is required for the surface-drift check." >&2; exit 1
    fi
  }
  _get() {
    local url="$1"
    if command -v curl >/dev/null 2>&1; then
      curl -sf -H "Accept: application/json" "$url"
    else
      wget -qO- "$url"
    fi
  }
  # Extracts the first string value of a JSON key (no jq dependency)
  _extract() {
    local json="$1" key="$2"
    printf '%s' "$json" | \
      grep -o "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | \
      head -1 | sed 's/.*":[[:space:]]*"//' | sed 's/"//'
  }

  # (a) Upload the perf workflow definition(s).
  # The loan-chain scenario chains app → disbursement, so the disbursement
  # definition must be uploaded first (the application's nextWorkflowId
  # references it).
  if [ "$PERF_SCENARIO" = "loan-chain-full" ]; then
    DISB_DEF_FILE="$SCRIPT_DIR/definitions/loan-chain/workflow-loan-disbursement.json"
    APP_DEF_FILE="$SCRIPT_DIR/definitions/loan-chain/workflow-loan-application.json"

    DISB_RESP="$(_post "${PERF_ENGINE_BASE_URL}/v1/definitions" "$(cat "$DISB_DEF_FILE")")"
    DISB_DEFINITION_ID="$(_extract "$DISB_RESP" "id")"
    if [ -z "$DISB_DEFINITION_ID" ]; then
      echo "[perf] ERROR: surface_drift — uploading loan-disbursement definition returned no 'id'." >&2
      echo "[perf]   response: ${DISB_RESP}" >&2
      exit 1
    fi
    echo "[perf]   uploaded loan-disbursement definition: id=${DISB_DEFINITION_ID}"

    APP_RESP="$(_post "${PERF_ENGINE_BASE_URL}/v1/definitions" "$(cat "$APP_DEF_FILE")")"
    APP_DEFINITION_ID="$(_extract "$APP_RESP" "id")"
    if [ -z "$APP_DEFINITION_ID" ]; then
      echo "[perf] ERROR: surface_drift — uploading loan-application definition returned no 'id'." >&2
      echo "[perf]   response: ${APP_RESP}" >&2
      exit 1
    fi
    echo "[perf]   uploaded loan-application definition: id=${APP_DEFINITION_ID}"

    DEFINITION_ID="$APP_DEFINITION_ID"
    export PERF_LOAN_APP_DEFINITION_ID="$APP_DEFINITION_ID"
    export PERF_LOAN_DISB_DEFINITION_ID="$DISB_DEFINITION_ID"
  else
    PERF_DEF_FILE="$SCRIPT_DIR/definitions/service-chain.json"
    DEFINITION_JSON="$(cat "$PERF_DEF_FILE")"
    UPLOAD_RESP="$(_post "${PERF_ENGINE_BASE_URL}/v1/definitions" "$DEFINITION_JSON")"
    DEFINITION_ID="$(_extract "$UPLOAD_RESP" "id")"
    if [ -z "$DEFINITION_ID" ]; then
      echo "[perf] ERROR: surface_drift — POST /v1/definitions returned no 'id' field." >&2
      echo "[perf]   response: ${UPLOAD_RESP}" >&2
      exit 1
    fi
    echo "[perf]   uploaded definition: id=${DEFINITION_ID}"
    export PERF_DEFINITION_ID="$DEFINITION_ID"
  fi

  # (b) Canary workflow start (POST /v1/instances)
  # For the loan-chain scenario, pass straight-through variables so the
  # canary's chained disbursement also auto-completes — otherwise the canary
  # would leave a senior-approval-task waiting and the teardown
  # "no non-terminal instances" check would (correctly) fail.
  if [ "$PERF_SCENARIO" = "loan-chain-full" ]; then
    START_BODY="{\"definitionId\":\"${DEFINITION_ID}\",\"businessKey\":\"perf-canary-${RUN_ID}\",\"variables\":{\"creditScore\":700,\"fraudScore\":0.0,\"loanAmount\":100000}}"
  else
    START_BODY="{\"definitionId\":\"${DEFINITION_ID}\",\"businessKey\":\"perf-canary-${RUN_ID}\"}"
  fi
  START_RESP="$(_post "${PERF_ENGINE_BASE_URL}/v1/instances" "$START_BODY")"
  CANARY_ID="$(_extract "$START_RESP" "id")"
  if [ -z "$CANARY_ID" ]; then
    echo "[perf] ERROR: surface_drift — POST /v1/instances returned no 'id' field." >&2
    echo "[perf]   response: ${START_RESP}" >&2
    exit 1
  fi

  # (c) Poll canary instance and verify status is a known value (GET /v1/instances/{id})
  POLL_RESP="$(_get "${PERF_ENGINE_BASE_URL}/v1/instances/${CANARY_ID}")"
  STATUS="$(_extract "$POLL_RESP" "status")"
  case "$STATUS" in
    ACTIVE|WAITING|COMPLETED|FAILED|CANCELLED) ;;
    *)
      echo "[perf] ERROR: surface_drift — GET /v1/instances/{id} returned unrecognised status: '${STATUS}'." >&2
      echo "[perf]   known: ACTIVE WAITING COMPLETED FAILED CANCELLED" >&2
      echo "[perf]   response: ${POLL_RESP}" >&2
      exit 1
      ;;
  esac
  echo "[perf] surface drift check: OK (canary status=${STATUS})"
}
surface_drift_check

# --- k6 scenario ---
if [ "$PERF_SCENARIO" = "loan-chain-full" ]; then
  if [ "$PERF_GRPC" = "true" ]; then
    SCENARIO="$SCRIPT_DIR/scenarios/loan-chain-full-grpc.js"
  else
    SCENARIO="$SCRIPT_DIR/scenarios/loan-chain-full.js"
  fi
elif [ "$PERF_GRPC" = "true" ]; then
  SCENARIO="$SCRIPT_DIR/scenarios/thousand-concurrent-grpc.js"
else
  SCENARIO="$SCRIPT_DIR/scenarios/thousand-concurrent.js"
fi
STARTED_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
HOST_STATS_PID=""
K6_PROC_PID=""

if [ ! -f "$SCENARIO" ]; then
  echo "[perf] WARNING: scenario not found at ${SCENARIO}" >&2
  PERF_K6_EXIT=0
else
  SCENARIO_RELPATH="perf/scenarios/$(basename "$SCENARIO")"
  SCENARIO_SHA="$(cd "$REPO_ROOT" && git log -1 --format='%H' -- "$SCENARIO_RELPATH" 2>/dev/null || echo 'unknown')"
  SCENARIO_LABEL="$(basename "$SCENARIO" .js)"
  echo "[perf] running scenario ${SCENARIO_LABEL} (${PERF_TARGET_VUS} VUs, scenarioSha=${SCENARIO_SHA})..."

  # T033: load threshold overrides JSON if --threshold-overrides was supplied
  PERF_THRESHOLD_OVERRIDES_JSON=""
  if [ -n "$PERF_THRESHOLD_OVERRIDES" ]; then
    if [ -f "$PERF_THRESHOLD_OVERRIDES" ]; then
      PERF_THRESHOLD_OVERRIDES_JSON="$(cat "$PERF_THRESHOLD_OVERRIDES")"
      echo "[perf] threshold overrides loaded from: $PERF_THRESHOLD_OVERRIDES"
    else
      echo "[perf] ERROR: --threshold-overrides file not found: $PERF_THRESHOLD_OVERRIDES" >&2
      exit 1
    fi
  fi

  # Shared k6 env args (used by both host and container invocation)
  K6_ENV_ARGS="\
    -e PERF_ENGINE_BASE_URL=${PERF_ENGINE_BASE_URL} \
    -e PERF_DEFINITION_ID=${PERF_DEFINITION_ID} \
    -e PERF_RUN_ID=${PERF_RUN_ID} \
    -e PERF_RAMP_UP=${PERF_RAMP_UP} \
    -e PERF_STEADY=${PERF_STEADY} \
    -e PERF_RAMP_DOWN=${PERF_RAMP_DOWN} \
    -e PERF_TARGET_VUS=${PERF_TARGET_VUS}"
  if [ -n "$PERF_THRESHOLD_OVERRIDES_JSON" ]; then
    K6_ENV_ARGS="$K6_ENV_ARGS -e PERF_THRESHOLD_OVERRIDES_JSON=${PERF_THRESHOLD_OVERRIDES_JSON}"
  fi
  if [ "$PERF_GRPC" = "true" ]; then
    K6_ENV_ARGS="$K6_ENV_ARGS \
      -e PERF_ENGINE_GRPC_URL=${PERF_ENGINE_GRPC_URL} \
      -e PERF_PROTO_ROOT=${PERF_PROTO_ROOT}"
  fi

  # Run k6 in background so we can capture its PID for host-stats sampling.
  set +e
  if [ "$PERF_CONTAINERIZED_K6" = "true" ]; then
    # T035: run k6 inside a container on the perf-net network.
    # The engine is reachable at http://engine:8080 (REST) or engine:9090 (gRPC) inside the network.
    K6_BASE_URL_CONTAINER="http://engine:8080"
    K6_SCENARIO_BASENAME="$(basename "$SCENARIO")"
    DOCKER_GRPC_ARGS=""
    if [ "$PERF_GRPC" = "true" ]; then
      DOCKER_GRPC_ARGS="-v ${REPO_ROOT}/proto:/proto:ro -e PERF_ENGINE_GRPC_URL=engine:9090 -e PERF_PROTO_ROOT=/proto"
    fi
    # shellcheck disable=SC2086
    docker run --rm \
      --network perf-net \
      -v "${SCRIPT_DIR}/scenarios:/scenarios:ro" \
      -v "${SCRIPT_DIR}/lib:/lib:ro" \
      -v "${REPORTS_DIR}:/reports" \
      $DOCKER_GRPC_ARGS \
      -e "PERF_ENGINE_BASE_URL=${K6_BASE_URL_CONTAINER}" \
      -e "PERF_DEFINITION_ID=${PERF_DEFINITION_ID}" \
      -e "PERF_RUN_ID=${PERF_RUN_ID}" \
      -e "PERF_RAMP_UP=${PERF_RAMP_UP}" \
      -e "PERF_STEADY=${PERF_STEADY}" \
      -e "PERF_RAMP_DOWN=${PERF_RAMP_DOWN}" \
      -e "PERF_TARGET_VUS=${PERF_TARGET_VUS}" \
      ${PERF_LOAN_APP_DEFINITION_ID:+-e "PERF_LOAN_APP_DEFINITION_ID=${PERF_LOAN_APP_DEFINITION_ID}"} \
      ${PERF_LOAN_DISB_DEFINITION_ID:+-e "PERF_LOAN_DISB_DEFINITION_ID=${PERF_LOAN_DISB_DEFINITION_ID}"} \
      ${PERF_THRESHOLD_OVERRIDES_JSON:+-e "PERF_THRESHOLD_OVERRIDES_JSON=${PERF_THRESHOLD_OVERRIDES_JSON}"} \
      "grafana/k6:${PERF_K6_VERSION_PIN}.0" run \
      --out "json=/reports/k6-output.json" \
      --summary-export "/reports/k6-summary-export.json" \
      "/scenarios/${K6_SCENARIO_BASENAME}" &
    K6_PROC_PID=$!
  else
    k6 run \
      --out "json=${REPORTS_DIR}/k6-output.json" \
      --summary-export "${REPORTS_DIR}/k6-summary-export.json" \
      -e "PERF_ENGINE_BASE_URL=${PERF_ENGINE_BASE_URL}" \
      -e "PERF_DEFINITION_ID=${PERF_DEFINITION_ID}" \
      -e "PERF_RUN_ID=${PERF_RUN_ID}" \
      -e "PERF_RAMP_UP=${PERF_RAMP_UP}" \
      -e "PERF_STEADY=${PERF_STEADY}" \
      -e "PERF_RAMP_DOWN=${PERF_RAMP_DOWN}" \
      -e "PERF_TARGET_VUS=${PERF_TARGET_VUS}" \
      ${PERF_LOAN_APP_DEFINITION_ID:+-e "PERF_LOAN_APP_DEFINITION_ID=${PERF_LOAN_APP_DEFINITION_ID}"} \
      ${PERF_LOAN_DISB_DEFINITION_ID:+-e "PERF_LOAN_DISB_DEFINITION_ID=${PERF_LOAN_DISB_DEFINITION_ID}"} \
      ${PERF_THRESHOLD_OVERRIDES_JSON:+-e "PERF_THRESHOLD_OVERRIDES_JSON=${PERF_THRESHOLD_OVERRIDES_JSON}"} \
      "$SCENARIO" &
    K6_PROC_PID=$!
  fi

  # Start host-stats sampler in background (T020 / research R6).
  # Samples docker container CPU/mem + k6 process every 5 s.
  sh "$SCRIPT_DIR/lib/host-stats.sh" "$REPORTS_DIR" "$K6_PROC_PID" 5 &
  HOST_STATS_PID=$!

  # Wait for k6 to finish
  wait "$K6_PROC_PID"
  PERF_K6_EXIT=$?
  set -e

  # Stop the host-stats sampler; let it write its closing summary block (T020).
  kill "$HOST_STATS_PID" 2>/dev/null || true
  wait "$HOST_STATS_PID" 2>/dev/null || true
  HOST_STATS_PID=""
fi

ENDED_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

# --- T023: engine /metrics snapshot before compose teardown ---
# Must happen here — after k6 but before the cleanup trap fires docker compose down.
METRICS_SNAPSHOT="${REPORTS_DIR}/engine-metrics-snapshot.txt"
if curl -sf "http://localhost:${PERF_ENGINE_METRICS_PORT}/metrics" \
       -o "$METRICS_SNAPSHOT" 2>/dev/null; then
  echo "[perf] engine metrics snapshot saved ($(wc -l < "$METRICS_SNAPSHOT") lines)"
else
  echo "[perf] WARNING: could not snapshot engine /metrics (engine may have exited early)"
fi

# --- T014: derive pass/fail from k6 exit code ---
if [ "${PERF_K6_EXIT:-0}" -eq 0 ]; then
  PERF_RESULT="pass"
else
  PERF_RESULT="fail"
fi
export PERF_RESULT

# --- T022/T024/T025: write full summary.json and summary.md ---
write_full_report() {
  local scenario_sha="${SCENARIO_SHA:-unknown}"
  local scenario_label="${SCENARIO_LABEL:-thousand-concurrent}"
  local engine_transport
  engine_transport="$([ "$PERF_GRPC" = "true" ] && echo grpc || echo rest)"
  local baseline_arg=""

  if [ -n "$PERF_COMPARE_TO" ]; then
    baseline_arg="$SCRIPT_DIR/reports/${PERF_COMPARE_TO}/summary.json"
  elif [ "$PERF_COMPARE_TO_LATEST" = "true" ]; then
    baseline_arg="$(find "$SCRIPT_DIR/reports" -name 'summary.json' \
      -exec grep -l '"result":"pass"' {} \; 2>/dev/null | \
      sort | tail -1)"
  fi

  REPORT_PATCH="{}"
  if command -v node >/dev/null 2>&1; then
    REPORT_PATCH="$(node "$SCRIPT_DIR/lib/report.js" "$REPORTS_DIR" "$baseline_arg" 2>/dev/null || echo '{}')"
  fi

  HOST_OS="$(uname -sr 2>/dev/null || echo 'unknown')"
  HOST_CPU_CORES="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo '0')"
  HOST_MEM_MB="$(awk '/MemTotal/{printf "%d",$2/1024}' /proc/meminfo 2>/dev/null || echo '0')"
  HOST_DOCKER_VER="$(docker version --format '{{.Client.Version}}' 2>/dev/null || echo 'unknown')"
  HOST_K6_VER="$(k6 version 2>/dev/null | head -1 || echo 'unknown')"

  export PERF_STARTED_AT="$STARTED_AT"
  export PERF_ENDED_AT="$ENDED_AT"
  export PERF_SCENARIO_LABEL="$scenario_label"
  export PERF_SCENARIO_SHA="$scenario_sha"
  export PERF_ENGINE_SHA="$ENGINE_SHA"
  export PERF_ENGINE_TRANSPORT="$engine_transport"
  export PERF_HOST_OS="$HOST_OS"
  export PERF_HOST_CPU_CORES="$HOST_CPU_CORES"
  export PERF_HOST_MEM_MB="$HOST_MEM_MB"
  export PERF_HOST_DOCKER_VER="$HOST_DOCKER_VER"
  export PERF_HOST_K6_VER="$HOST_K6_VER"
  export PERF_REPORT_PATCH="$REPORT_PATCH"

  python3 "$SCRIPT_DIR/lib/generate-summary-json.py" "$REPORTS_DIR"
  python3 "$SCRIPT_DIR/lib/render-report-md.py" "$REPORTS_DIR"
}

write_full_report

# --- Final output ---
echo "[perf] ${PERF_RESULT} — runId=${RUN_ID}"
echo "[perf] report: ${REPORTS_DIR}/summary.md"
if [ "$PERF_RESULT" = "fail" ]; then
  echo "[perf] see Breached thresholds in summary.md for details"
fi

# cleanup trap fires on exit → docker compose down -v
# PERF_K6_EXIT is passed through to the process exit code.
