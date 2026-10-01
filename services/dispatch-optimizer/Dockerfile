# Build from the repo root: docker build -f services/dispatch-optimizer/Dockerfile .
# One image, two entrypoints: the API (default) and the outbox relay (compose overrides the command).
# ponytail: base images pinned by tag; pin by digest in the DevOps hardening step (CLAUDE.md §19).
FROM ghcr.io/astral-sh/uv:0.9-python3.12-bookworm-slim AS build
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
# Same path in both stages: venv scripts embed absolute shebangs.
WORKDIR /srv/services/dispatch-optimizer
COPY libs/proto/gen/python /srv/libs/proto/gen/python
COPY services/dispatch-optimizer/pyproject.toml services/dispatch-optimizer/uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY services/dispatch-optimizer/app ./app
COPY services/dispatch-optimizer/config ./config

FROM python:3.12-slim-bookworm
RUN useradd --system --uid 10001 app
WORKDIR /srv/services/dispatch-optimizer
COPY --from=build /srv/services/dispatch-optimizer /srv/services/dispatch-optimizer
COPY data/reference/tariffs.csv /srv/data/reference/tariffs.csv
ENV PATH=/srv/services/dispatch-optimizer/.venv/bin:$PATH PYTHONUNBUFFERED=1 KILOWATT_ROOT=/srv
USER 10001
EXPOSE 8002
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8002/healthz', timeout=2)"]
CMD ["uvicorn", "--factory", "app.main:app", "--host", "0.0.0.0", "--port", "8002"]
