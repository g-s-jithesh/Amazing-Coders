"""Recompute the gateway e2e accounting from the raw files in this folder: python summarise.py"""
import re
from pathlib import Path

here = Path(__file__).parent


def metrics(name):
    out = {}
    for line in (here / name).read_text().splitlines():
        if line and not line.startswith("#"):
            k, v = line.rsplit(" ", 1)
            out[k] = float(v)
    return out


def offsets(name):
    tot = {}
    for line in (here / name).read_text().split():
        topic, _, off = line.rsplit(":", 2)
        tot[topic] = tot.get(topic, 0) + int(off)
    return tot


mb, ma = metrics("metrics_before.txt"), metrics("metrics_after.txt")
d = lambda k: int(ma.get(k, 0) - mb.get(k, 0))
ob, oa = offsets("offsets_before.txt"), offsets("offsets_after.txt")
od = {t: oa[t] - ob.get(t, 0) for t in oa}

published = int(re.findall(r"published=(\d+)", (here / "sim.log").read_text())[-1])
https_dropped = int(re.findall(r"dropped_records=(\d+)", (here / "sim.log").read_text())[-1])
accepted = sum(d(f'ingest_events_total{{oem="{o}",result="accepted"}}') for o in ("oem_a", "oem_b", "oem_c"))
rejected = sum(d(f'ingest_events_total{{oem="{o}",result="rejected"}}') for o in ("oem_a", "oem_b", "oem_c"))
dups = d("dedup_hits_total")
reasons = {k.split('"')[1]: d(k) for k in ma if k.startswith("dlq_total{") and d(k)}

# produce latency p50/p95/p99 upper bounds from the histogram delta
buckets = sorted((float(k.split('le="')[1].split('"')[0]), d(k)) for k in ma
                 if k.startswith("produce_latency_seconds_bucket") and "+Inf" not in k)
count = d("produce_latency_seconds_count")
def q(p):
    for le, c in buckets:
        if c >= p * count:
            return le
    return float("inf")

print(f"simulator published            = {published}   (https dropped: {https_dropped})")
print(f"gateway accepted+rejected+dups = {accepted + rejected + dups}  = {accepted} + {rejected} + {dups}")
print(f"unaccounted                    = {published - https_dropped - accepted - rejected - dups}")
print(f"canonical offset delta         = {od['telemetry.canonical.v1']}  (accepted {accepted})")
print(f"dlq offset delta               = {od['telemetry.dlq.v1']}  (rejected {rejected})")
print(f"raw offset deltas              = { {t: v for t, v in od.items() if t.startswith('oem.raw')} }")
print(f"dlq reasons                    = {reasons}")
print(f"duplicate share of published   = {dups / published:.4%}")
print(f"produce latency (per message, until all acks) p50 <= {q(.5)*1000:.0f} ms, p95 <= {q(.95)*1000:.0f} ms, p99 <= {q(.99)*1000:.0f} ms over {count} messages")
