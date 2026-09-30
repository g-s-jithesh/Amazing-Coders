#!/usr/bin/env bash
# End-to-end local run: simulator → gateway → Kafka → stream-processor (processor + raw-sink roles).
# Injects a demo fault with a short precursor, records every alert, both consumer groups' lag, and
# before/after metrics of the gateway and both processor roles. Writes to OUT (a local, non-synced
# directory; copy into docs/evidence afterwards — see .claude/skills/evidence/SKILL.md).
#
#   OUT=/tmp/kw-e2e DURATION=180s VIN=0KCDV45N0MB000239 tests/load/e2e-local.sh
set -euo pipefail
export MSYS_NO_PATHCONV=1

OUT=${OUT:?set OUT to a local output directory}
DURATION=${DURATION:-180s}
VIN=${VIN:-0KCDV45N0MB000239}
FAULT=${FAULT:-cooling_degradation}
PRECURSOR=${PRECURSOR:-2m}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

lag() {
  docker exec kilowatt-kafka-1 /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
    --describe --group "$1" 2>/dev/null | awk '$2=="telemetry.canonical.v1"{s+=$6} END{print s+0}'
}
snap() { # $1 = before|after
  curl -s localhost:8081/metrics > "$OUT/gw_metrics_$1.txt"
  curl -s localhost:9102/metrics > "$OUT/sp_metrics_$1.txt"
  curl -s localhost:9103/metrics > "$OUT/raw_metrics_$1.txt"
}
wait_drained() {
  for _ in $(seq 1 120); do
    [ "$(lag stream-processor)" = "0" ] && [ "$(lag stream-processor-raw)" = "0" ] && return 0
    sleep 3
  done
}

mkdir -p "$OUT"
cd "$ROOT"
python .claude/skills/evidence/capture_env.py "$OUT" > /dev/null 2>&1 || true
echo "$VIN" > "$OUT/demo_vin.txt"
TOOLS=$(mktemp -d)
(cd services/stream-processor && go build -o "$TOOLS/alerts-tail" ./cmd/alerts-tail)

wait_drained
echo "pre-run lag processor=$(lag stream-processor) raw=$(lag stream-processor-raw)" | tee "$OUT/run.txt"
snap before
secs=${DURATION%s}
"$TOOLS/alerts-tail" --from end --for "$((secs + 120))s" > "$OUT/alerts.jsonl" 2>&1 &
tail_pid=$!
( for _ in $(seq 1 $(((secs + 120) / 3))); do echo "$(date +%s) $(lag stream-processor) $(lag stream-processor-raw)"; sleep 3; done ) > "$OUT/lag.txt" 2>/dev/null &
lag_pid=$!
sleep 3

make sim DURATION="$DURATION" INJECT="$VIN:$FAULT" PRECURSOR="$PRECURSOR" > "$OUT/sim.log" 2>&1
wait_drained
echo "post-run lag processor=$(lag stream-processor) raw=$(lag stream-processor-raw)" | tee -a "$OUT/run.txt"
sleep 5
snap after
docker exec kilowatt-scylla-1 cqlsh -e "SELECT count(*) FROM kilowatt.telemetry_raw WHERE vin='$VIN' AND day='$(date -u +%Y-%m-%d)';" > "$OUT/scylla_demo_vin.txt" 2>&1 || true
docker exec kilowatt-redis-1 redis-cli HGETALL "vehicle:$VIN:state" > "$OUT/redis_demo_vin.txt" || true
docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' > "$OUT/docker_stats_after.txt"
kill "$tail_pid" "$lag_pid" 2>/dev/null || true
cp "$ROOT/tests/load/summarise_e2e.py" "$OUT/summarise.py"
python "$OUT/summarise.py" | tee "$OUT/summarise.out"
