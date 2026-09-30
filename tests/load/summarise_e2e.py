"""Summarise an e2e-local.sh run from its raw files: python summarise.py (run inside the output folder)."""
import json
import re
from pathlib import Path

here = Path(__file__).parent


def metrics(name):
    out = {}
    p = here / name
    if not p.exists():
        return out
    for line in p.read_text().splitlines():
        if line and not line.startswith("#"):
            k, v = line.rsplit(" ", 1)
            out[k] = float(v)
    return out


def delta(prefix):
    b, a = metrics(f"{prefix}_metrics_before.txt"), metrics(f"{prefix}_metrics_after.txt")
    return lambda k: a.get(k, 0) - b.get(k, 0), a, b


def hist_quantiles(d, a, name, label=""):
    buckets = sorted((float(k.split('le="')[1].split('"')[0]), d(k)) for k in a
                     if k.startswith(name + "_bucket") and label in k and "+Inf" not in k)
    n = d(f"{name}_count{('{' + label + '}') if label else ''}")
    def q(p):
        for le, c in buckets:
            if n and c >= p * n:
                return le
        return float("inf")
    return n, q(0.5), q(0.95), q(0.99)


sim = (here / "sim.log").read_text()
published = int(re.findall(r"published=(\d+)", sim)[-1])
dropped = int((re.findall(r"dropped_records=(\d+)", sim) or ["0"])[-1])
gw, gwa, _ = delta("gw")
accepted = sum(gw(f'ingest_events_total{{oem="{o}",result="accepted"}}') for o in ("oem_a", "oem_b", "oem_c"))
rejected = sum(gw(f'ingest_events_total{{oem="{o}",result="rejected"}}') for o in ("oem_a", "oem_b", "oem_c"))
dups = gw("dedup_hits_total")
sp, spa, _ = delta("sp")
raw, rawa, _ = delta("raw")

print(f"published {published}  https_dropped {dropped}")
print(f"gateway accepted {accepted:.0f} rejected {rejected:.0f} duplicates {dups:.0f}  unaccounted {published - dropped - accepted - rejected - dups:.0f}")
print(f"processor events {sp('sp_events_total'):.0f}" + (f"  raw-sink events {raw('sp_events_total'):.0f}" if rawa else ""))

vin = (here / "demo_vin.txt").read_text().strip()
for line in (here / "alerts.jsonl").read_text().splitlines():
    try:
        a = json.loads(line)
    except json.JSONDecodeError:
        continue
    if a.get("vin") == vin:
        t = int(a["triggerIngestMs"])
        print(f"demo alert {a['ruleId']} {a.get('detail', '')} {a['severity']}: gateway-ingest -> processed "
              f"{int(a['processedMs']) - t} ms, -> Kafka append {a['kafkaAppendMs'] - t} ms")

n, p50, p95, p99 = hist_quantiles(sp, spa, "alert_latency_seconds")
print(f"all alerts with a trigger (n={n:.0f}): latency <= p50 {p50} s, p95 {p95} s, p99 {p99} s (bucket upper bounds)")
for s, (d, a) in {"kafka": (sp, spa), "redis": (sp, spa), "scylla": (raw, rawa) if rawa else (sp, spa)}.items():
    c, tot = d(f'sink_write_seconds_count{{sink="{s}"}}'), d(f'sink_write_seconds_sum{{sink="{s}"}}')
    print(f"sink {s}: {c:.0f} batch writes, mean {tot / max(c, 1) * 1000:.0f} ms")
lagf = here / "lag.txt"
lags = [l.split() for l in lagf.read_text().splitlines()] if lagf.exists() else []
if lags:  # older runs had one group (2 columns); split-role runs have processor + raw-sink (3 columns)
    print(f"max lag processor {max(int(x[1]) for x in lags if len(x) > 1)}"
          + (f"  raw-sink {max(int(x[2]) for x in lags if len(x) > 2)}" if any(len(x) > 2 for x in lags) else ""))
