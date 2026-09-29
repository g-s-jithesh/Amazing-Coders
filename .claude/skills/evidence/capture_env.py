#!/usr/bin/env python3
"""Capture the environment for an evidence run (stdlib only, Linux/macOS/Windows).

Usage: python3 capture_env.py <output_dir>
Writes <output_dir>/env.json and <output_dir>/env.md.
"""
from __future__ import annotations

import datetime as dt
import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path


def sh(cmd: list[str], timeout: int = 15) -> str | None:
    if shutil.which(cmd[0]) is None:
        return None
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        return out.stdout.strip() if out.returncode == 0 else None
    except (subprocess.TimeoutExpired, OSError):
        return None


def cpu_model() -> str | None:
    system = platform.system()
    if system == "Linux":
        try:
            for line in Path("/proc/cpuinfo").read_text().splitlines():
                if line.lower().startswith("model name"):
                    return line.split(":", 1)[1].strip()
        except OSError:
            pass
    if system == "Darwin":
        return sh(["sysctl", "-n", "machdep.cpu.brand_string"])
    if system == "Windows":
        return platform.processor() or None
    return platform.processor() or None


def ram_gb() -> float | None:
    system = platform.system()
    try:
        if system == "Linux":
            for line in Path("/proc/meminfo").read_text().splitlines():
                if line.startswith("MemTotal:"):
                    return round(int(line.split()[1]) / 1024 / 1024, 1)
        if system == "Darwin":
            v = sh(["sysctl", "-n", "hw.memsize"])
            return round(int(v) / 1024**3, 1) if v else None
        if system == "Windows":
            import ctypes

            class MEMSTAT(ctypes.Structure):
                _fields_ = [("dwLength", ctypes.c_ulong), ("dwMemoryLoad", ctypes.c_ulong),
                            ("ullTotalPhys", ctypes.c_ulonglong), ("ullAvailPhys", ctypes.c_ulonglong),
                            ("ullTotalPageFile", ctypes.c_ulonglong), ("ullAvailPageFile", ctypes.c_ulonglong),
                            ("ullTotalVirtual", ctypes.c_ulonglong), ("ullAvailVirtual", ctypes.c_ulonglong),
                            ("sullAvailExtendedVirtual", ctypes.c_ulonglong)]
            m = MEMSTAT()
            m.dwLength = ctypes.sizeof(MEMSTAT)
            ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(m))  # type: ignore[attr-defined]
            return round(m.ullTotalPhys / 1024**3, 1)
    except Exception:
        return None
    return None


def main() -> None:
    if len(sys.argv) != 2:
        print(__doc__)
        sys.exit(1)
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)

    docker_info = sh(["docker", "info", "--format", "{{json .}}"])
    docker = None
    if docker_info:
        try:
            d = json.loads(docker_info)
            docker = {"server_version": d.get("ServerVersion"), "ncpu": d.get("NCPU"),
                      "mem_gb": round(d.get("MemTotal", 0) / 1024**3, 1), "os": d.get("OperatingSystem")}
        except json.JSONDecodeError:
            pass

    env = {
        "captured_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"),
        "git": {
            "sha": sh(["git", "rev-parse", "HEAD"]),
            "branch": sh(["git", "rev-parse", "--abbrev-ref", "HEAD"]),
            "dirty": bool(sh(["git", "status", "--porcelain"])),
        },
        "host": {
            "os": f"{platform.system()} {platform.release()}",
            "machine": platform.machine(),
            "cpu_model": cpu_model(),
            "logical_cpus": os.cpu_count(),
            "ram_gb": ram_gb(),
            "python": platform.python_version(),
        },
        "docker": docker,
        "kubernetes": {
            "context": sh(["kubectl", "config", "current-context"]),
            "nodes": sh(["kubectl", "get", "nodes", "-o", "wide", "--no-headers"], timeout=10),
        },
        "tool_versions": {
            "go": sh(["go", "version"]),
            "node": sh(["node", "--version"]),
            "k6": sh(["k6", "version"]),
        },
    }
    (out / "env.json").write_text(json.dumps(env, indent=2))

    h, g = env["host"], env["git"]
    md = [
        "# Environment",
        "",
        f"- Captured (UTC): {env['captured_at_utc']}",
        f"- Git: `{g['sha']}` on `{g['branch']}`{' **(dirty working tree)**' if g['dirty'] else ''}",
        f"- Host: {h['os']} / {h['machine']} / {h['cpu_model']} / {h['logical_cpus']} logical CPUs / {h['ram_gb']} GB RAM",
        f"- Docker: {docker if docker else 'not available'}",
        f"- Kubernetes context: {env['kubernetes']['context'] or 'none'}",
    ]
    if env["kubernetes"]["nodes"]:
        md += ["", "```", env["kubernetes"]["nodes"], "```"]
    md += [
        "",
        "## Run configuration (fill in)",
        "",
        "- Vehicles / rate Hz / simulator mode:",
        "- Kafka brokers / partitions (telemetry.canonical.v1) / replication:",
        "- Service replicas and CPU/memory limits:",
        "- Dataset size (rows in relevant tables):",
        "",
    ]
    (out / "env.md").write_text("\n".join(md))
    print(f"wrote {out/'env.json'} and {out/'env.md'}")
    if g["dirty"]:
        print("WARNING: working tree is dirty; commit first so the run is reproducible.", file=sys.stderr)


if __name__ == "__main__":
    main()
