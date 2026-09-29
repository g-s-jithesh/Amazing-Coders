#!/usr/bin/env python3
"""PostToolUse hook: format the file Claude just edited, then run fast checks.

- Formats: ruff (Python), goimports/gofmt (Go), prettier + eslint (web/), buf (proto), terraform fmt (tf).
- Checks: remaining ruff lint errors, and the layering rules from CLAUDE.md §12
  (domain/ must not import frameworks or infrastructure; application/ must not import adapters).
- Missing tools are skipped silently, so the hook works before the toolchain is installed.
- On problems: exit 2 and send them to Claude (stderr) so it fixes them in the next step.
"""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

PROJECT_DIR = Path(os.environ.get("CLAUDE_PROJECT_DIR", os.getcwd()))
TIMEOUT = 45

PY_DOMAIN_FORBIDDEN = re.compile(
    r"^\s*(?:from|import)\s+(fastapi|starlette|sqlalchemy|sqlmodel|alembic|confluent_kafka|aiokafka|redis|"
    r"boto3|botocore|minio|cassandra|httpx|requests|aiohttp|langchain\w*|langgraph|psycopg\w*|asyncpg|pyspark|"
    r"pydantic_settings|opentelemetry|mcp)\b",
    re.M,
)
PY_LAYER_UP = {
    "domain": re.compile(r"^\s*(?:from|import)\s+(?:[\w.]*\.)?(?:application|infrastructure|api|workers)\b|^\s*from\s+\.+(?:application|infrastructure|api|workers)\b", re.M),
    "application": re.compile(r"^\s*(?:from|import)\s+(?:[\w.]*\.)?(?:infrastructure|api|workers)\b|^\s*from\s+\.+(?:infrastructure|api|workers)\b", re.M),
}
GO_DOMAIN_FORBIDDEN = (
    "net/http", "database/sql", "github.com/twmb/franz-go", "github.com/redis/", "github.com/gocql/",
    "github.com/scylladb/", "github.com/eclipse/paho", "github.com/aws/", "cloud.google.com/", "github.com/Azure/",
    "go.opentelemetry.io", "/internal/adapters", "/internal/app",
)
GO_APP_FORBIDDEN = ("/internal/adapters", "github.com/twmb/franz-go", "github.com/redis/", "github.com/gocql/",
                    "github.com/eclipse/paho")


def run(cmd: list[str], cwd: Path | None = None) -> subprocess.CompletedProcess[str] | None:
    if shutil.which(cmd[0]) is None and not Path(cmd[0]).exists():
        return None
    try:
        return subprocess.run(cmd, cwd=cwd or PROJECT_DIR, capture_output=True, text=True, timeout=TIMEOUT)
    except (subprocess.TimeoutExpired, OSError):
        return None


def layer_of(rel: str) -> str | None:
    parts = rel.split("/")
    for layer in ("domain", "application"):
        if layer in parts:
            return layer
    return None


def go_imports(src: str) -> list[str]:
    imports: list[str] = []
    for block in re.findall(r"^import\s*\((.*?)^\)", src, re.S | re.M):
        imports += re.findall(r"\"([^\"]+)\"", block)
    imports += re.findall(r"^import\s+(?:\w+\s+)?\"([^\"]+)\"", src, re.M)
    return imports


def check_layering(rel: str, path: Path) -> list[str]:
    layer = layer_of(rel)
    if not layer:
        return []
    src = path.read_text(encoding="utf-8", errors="ignore")
    problems: list[str] = []
    if path.suffix == ".py":
        if layer == "domain":
            for m in PY_DOMAIN_FORBIDDEN.finditer(src):
                problems.append(f"{rel}: domain/ imports framework/infrastructure module '{m.group(1)}'. "
                                "Define a port (Protocol) in domain/application and implement it in infrastructure/.")
        rx = PY_LAYER_UP.get(layer)
        if rx and rx.search(src):
            problems.append(f"{rel}: {layer}/ imports an outer layer. Allowed direction: api -> application -> domain; "
                            "infrastructure implements ports defined inward.")
    elif path.suffix == ".go" and not rel.endswith("_test.go"):
        forbidden = GO_DOMAIN_FORBIDDEN if layer == "domain" else GO_APP_FORBIDDEN
        for imp in go_imports(src):
            if any(f in imp for f in forbidden):
                problems.append(f"{rel}: internal/{layer} imports '{imp}'. Depend on an interface (port) instead; "
                                "wire the adapter in cmd/*/main.go.")
    return problems


def format_and_lint(rel: str, path: Path) -> list[str]:
    problems: list[str] = []
    ext = path.suffix
    if ext in (".py", ".pyi"):
        run(["ruff", "format", "--quiet", str(path)])
        run(["ruff", "check", "--fix", "--quiet", str(path)])
        res = run(["ruff", "check", "--output-format", "concise", "--quiet", str(path)])
        if res and res.returncode != 0 and res.stdout.strip():
            problems.append("ruff found issues it could not auto-fix:\n" + res.stdout.strip()[:3000])
    elif ext == ".go":
        res = run(["goimports", "-w", str(path)]) or run(["gofmt", "-w", str(path)])
        if res and res.returncode != 0:
            problems.append(f"gofmt failed (likely a syntax error) in {rel}:\n{res.stderr.strip()[:2000]}")
    elif rel.startswith("web/") and ext in (".ts", ".tsx", ".js", ".jsx", ".css", ".json", ".md", ".html"):
        bin_dir = PROJECT_DIR / "web" / "node_modules" / ".bin"
        prettier = bin_dir / ("prettier.cmd" if os.name == "nt" else "prettier")
        eslint = bin_dir / ("eslint.cmd" if os.name == "nt" else "eslint")
        if prettier.exists():
            run([str(prettier), "--write", "--log-level", "warn", str(path)], cwd=PROJECT_DIR / "web")
        if eslint.exists() and ext in (".ts", ".tsx"):
            res = run([str(eslint), "--fix", "--format", "unix", str(path)], cwd=PROJECT_DIR / "web")
            if res and res.returncode not in (0, None) and res.stdout.strip():
                problems.append("eslint errors:\n" + res.stdout.strip()[:3000])
    elif ext == ".proto":
        run(["buf", "format", "-w", str(path)])
    elif ext in (".tf", ".tfvars"):
        run(["terraform", "fmt", str(path)])
    return problems


def main() -> None:
    data = json.load(sys.stdin)
    ti = data.get("tool_input", {}) or {}
    file_path = ti.get("file_path")
    if not file_path:
        return
    path = Path(file_path)
    if not path.is_absolute():
        path = PROJECT_DIR / path
    if not path.exists() or ".claude/hooks" in path.as_posix():
        return
    try:
        rel = path.resolve().relative_to(PROJECT_DIR.resolve()).as_posix()
    except ValueError:
        rel = path.as_posix()

    problems = format_and_lint(rel, path) + check_layering(rel, path)
    if problems:
        print("[kilowatt post-edit] Fix these before moving on:\n- " + "\n- ".join(problems), file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except Exception as exc:
        print(f"[kilowatt post-edit] hook error (not blocking): {exc}", file=sys.stderr)
        sys.exit(1)
    sys.exit(0)
