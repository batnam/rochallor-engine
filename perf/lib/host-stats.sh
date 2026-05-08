#!/usr/bin/env sh
# Samples generator + container resource utilisation on a 5 s cadence.
# Writes streaming samples to <reports-dir>/host-stats.json and, on exit,
# appends a summary block (peaks + saturation flags).
#
# Usage: host-stats.sh <reports-dir> <k6-pid> [interval-seconds]
# Run in the background from run.sh; terminate via SIGTERM when k6 finishes.
#
# Contract: specs/003-performance-load-testing/contracts/run-report.md §2

set -e

REPORTS_DIR="${1:?reports-dir required}"
K6_PID="${2:?k6-pid required}"
INTERVAL="${3:-5}"
OUTPUT="${REPORTS_DIR}/host-stats.json"

# Write opening of JSON array
printf '{"schemaVersion":"1.0","intervalSeconds":%s,"samples":[' "$INTERVAL" > "$OUTPUT"

FIRST=1
TEST_START_MS="$(date -u '+%s')000"  # epoch ms (approximate)

# Accumulators for summary (peak tracking)
PEAK_K6_CPU=0
PEAK_ENG_CPU=0
PEAK_PG_CPU=0
PEAK_WRK_CPU=0
SAMPLE_COUNT=0

get_container_stats() {
  # docker stats --no-stream returns one line per container.
  # Format: NAME CPU% MEM
  docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' 2>/dev/null || true
}

get_k6_stats() {
  local pid="$1"
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    printf '0\t0\t0'
    return
  fi
  # ps output: %cpu %mem rss(KB)
  ps -o pcpu=,pmem=,rss= -p "$pid" 2>/dev/null | \
    awk '{printf "%.1f\t%.1f\t%d", $1, $2, $3}' || printf '0\t0\t0'
}

strip_pct() { printf '%s' "$1" | tr -d '%'; }

max_of() {
  local a="$1" b="$2"
  if awk "BEGIN{exit !($a > $b)}" 2>/dev/null; then printf '%s' "$a"; else printf '%s' "$b"; fi
}

write_sample() {
  local t_sec="$1"
  local k6_cpu="$2" k6_mem="$3" k6_rss="$4"
  local eng_cpu="$5" eng_mem="$6"
  local pg_cpu="$7"  pg_mem="$8"
  local wrk_cpu_sum="$9"
  local loadavg="$10"

  if [ "$FIRST" -eq 0 ]; then printf ',' >> "$OUTPUT"; fi
  FIRST=0

  cat >> "$OUTPUT" << SAMPLE
{
 "tSinceStartSec":${t_sec},
 "k6Process":{"cpuPct":${k6_cpu},"memPct":${k6_mem},"rssMB":$(awk "BEGIN{printf \"%.1f\",${k6_rss}/1024}")},
 "containers":{"engine":{"cpuPct":${eng_cpu},"memApprox":"${eng_mem}"},"postgres":{"cpuPct":${pg_cpu},"memApprox":"${pg_mem}"},"workersCombinedCpu":${wrk_cpu_sum}},
 "loadavg1":${loadavg}
}
SAMPLE

  # Update peaks
  PEAK_K6_CPU="$(max_of "$k6_cpu" "$PEAK_K6_CPU")"
  PEAK_ENG_CPU="$(max_of "$eng_cpu" "$PEAK_ENG_CPU")"
  PEAK_PG_CPU="$(max_of "$pg_cpu" "$PEAK_PG_CPU")"
  PEAK_WRK_CPU="$(max_of "$wrk_cpu_sum" "$PEAK_WRK_CPU")"
  SAMPLE_COUNT=$((SAMPLE_COUNT + 1))
}

write_summary() {
  # Saturation threshold: > 90% CPU peak → likely saturated
  sat_k6="false";  awk "BEGIN{exit !(${PEAK_K6_CPU:-0} > 90)}" 2>/dev/null && sat_k6="true"
  sat_eng="false"; awk "BEGIN{exit !(${PEAK_ENG_CPU:-0} > 90)}" 2>/dev/null && sat_eng="true"
  sat_pg="false";  awk "BEGIN{exit !(${PEAK_PG_CPU:-0} > 90)}" 2>/dev/null && sat_pg="true"

  cat >> "$OUTPUT" << SUMMARY
],"summary":{
 "sampleCount":${SAMPLE_COUNT},
 "k6CpuPctPeak":${PEAK_K6_CPU:-0},
 "engineCpuPctPeak":${PEAK_ENG_CPU:-0},
 "postgresCpuPctPeak":${PEAK_PG_CPU:-0},
 "workersCpuPctPeak":${PEAK_WRK_CPU:-0},
 "generatorLikelySaturated":${sat_k6},
 "engineLikelySaturated":${sat_eng},
 "postgresLikelySaturated":${sat_pg}
}}
SUMMARY
}

cleanup() {
  write_summary
}
trap cleanup EXIT TERM INT

# --- Sampling loop ---
while true; do
  NOW_SEC="$(date -u '+%s')"
  T_SINCE=$(( NOW_SEC * 1000 - TEST_START_MS / 1000 ))

  # k6 process stats
  K6_STATS="$(get_k6_stats "$K6_PID")"
  K6_CPU="$(printf '%s' "$K6_STATS" | cut -f1)"
  K6_MEM="$(printf '%s' "$K6_STATS" | cut -f2)"
  K6_RSS="$(printf '%s' "$K6_STATS" | cut -f3)"

  # Container stats
  CSTATS="$(get_container_stats)"
  ENG_CPU=0; ENG_MEM="n/a"; PG_CPU=0; PG_MEM="n/a"; WRK_CPU=0

  while IFS='	' read -r name cpu mem; do
    cpuv="$(strip_pct "$cpu")"
    cpuv="${cpuv:-0}"
    case "$name" in
      *engine*)
        ENG_CPU="$cpuv"; ENG_MEM="$mem" ;;
      *postgres*)
        PG_CPU="$cpuv";  PG_MEM="$mem"  ;;
      *worker*)
        WRK_CPU="$(awk "BEGIN{printf \"%.1f\",${WRK_CPU}+${cpuv}}")" ;;
    esac
  done << STATS
$CSTATS
STATS

  # Load average (first value)
  LOADAVG="$(awk '{print $1}' /proc/loadavg 2>/dev/null || uptime | awk -F'load average:' '{print $2}' | awk -F',' '{print $1}' | tr -d ' ')"
  LOADAVG="${LOADAVG:-0}"

  write_sample "$T_SINCE" \
    "${K6_CPU:-0}" "${K6_MEM:-0}" "${K6_RSS:-0}" \
    "${ENG_CPU:-0}" "${ENG_MEM:-n/a}" \
    "${PG_CPU:-0}"  "${PG_MEM:-n/a}" \
    "${WRK_CPU:-0}" \
    "${LOADAVG:-0}"

  sleep "$INTERVAL"
done
