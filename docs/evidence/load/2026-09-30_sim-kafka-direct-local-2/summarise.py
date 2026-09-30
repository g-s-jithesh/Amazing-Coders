"""Recompute the summary from raw files: python summarise.py (run inside this folder)."""
import re
from datetime import datetime
from pathlib import Path

here = Path(__file__).parent
stats = []
for line in (here / "raw.log").read_text(encoding="utf-8").splitlines():
    m = re.search(r"^(\S+ \S+) INFO stats .*published=(\d+) publish_errors=(\d+)", line)
    if m:
        stats.append((datetime.strptime(m[1], "%Y/%m/%d %H:%M:%S"), int(m[2]), int(m[3])))

def offsets(name):
    tot = {}
    for line in (here / name).read_text().split():
        topic, _, off = line.rsplit(":", 2)
        tot[topic] = tot.get(topic, 0) + int(off)
    return tot

before, after = offsets("offsets_before.txt"), offsets("offsets_after.txt")
delta = {t: after[t] - before.get(t, 0) for t in after}
published, errors = stats[-1][1], stats[-1][2]

# Steady-state throughput: from the first stats line (skips startup + first 10 s) to the last full 10 s window.
windows = [(b[1] - a[1]) / (b[0] - a[0]).total_seconds() for a, b in zip(stats, stats[1:]) if (b[0] - a[0]).total_seconds() >= 9]
steady = (stats[-2][1] - stats[0][1]) / (stats[-2][0] - stats[0][0]).total_seconds()

print(f"published_by_simulator = {published}")
print(f"publish_errors         = {errors}")
print(f"broker_offset_delta    = {sum(delta.values())}  {delta}")
print(f"loss                   = {published - sum(delta.values())}")
print(f"steady_state_eps       = {int(steady)}  (floor)")
print(f"10s_window_eps min/max = {int(min(windows))} / {int(max(windows))}")
