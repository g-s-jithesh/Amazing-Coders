# Build from the repo root: docker build -f services/battery-intel/Dockerfile .
# One image, two entrypoints: the API (default) and the session worker (compose overrides the command).
# ponytail: base images pinned by tag; pin by digest in the DevOps hardening step (CLAUDE.md §19).
FROM ghcr.io/astral-sh/uv:0.9-python3.12-bookworm-slim AS build
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
# Same path in both stages: venv scripts embed absolute shebangs.
WORKDIR /srv/services/battery-intel
COPY libs/proto/gen/python /srv/libs/proto/gen/python
COPY services/battery-intel/pyproject.toml services/battery-intel/uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY services/battery-intel/app ./app

FROM python:3.12-slim-bookworm
RUN useradd --system --uid 10001 app
WORKDIR /srv/services/battery-intel
COPY --from=build /srv/services/battery-intel /srv/services/battery-intel
COPY data/reference/dtc_catalogue.csv /ref/dtc_catalogue.csv
ENV PATH=/srv/services/battery-intel/.venv/bin:$PATH PYTHONUNBUFFERED=1 DTC_CATALOGUE=/ref/dtc_catalogue.csv
USER 10001
EXPOSE 8001
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8001/healthz', timeout=2)"]
CMD ["uvicorn", "--factory", "app.main:app", "--host", "0.0.0.0", "--port", "8001"]
