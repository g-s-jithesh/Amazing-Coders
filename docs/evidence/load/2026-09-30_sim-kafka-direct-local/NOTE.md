# Superseded: loss check not captured

The throughput run itself completed (see `raw.log`), but `offsets_before.txt` / `offsets_after.txt` hold an
error: Git Bash rewrote the container path of `kafka-get-offsets.sh`. Without the before/after end offsets this run
cannot prove zero loss, so it is **not** used for any reported number. Re-run with `MSYS_NO_PATHCONV=1` in
`../2026-09-30_sim-kafka-direct-local-2/`.
