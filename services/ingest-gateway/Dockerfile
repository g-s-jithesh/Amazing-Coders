# Build from the repo root (the gateway depends on libs/go-common and libs/proto):
#   docker build -f services/ingest-gateway/Dockerfile .
# ponytail: base images pinned by tag; pin by digest in the DevOps hardening step (CLAUDE.md §19).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY libs/go-common libs/go-common
COPY libs/proto libs/proto
COPY services/ingest-gateway/go.mod services/ingest-gateway/go.sum services/ingest-gateway/
RUN cd services/ingest-gateway && go mod download
COPY services/ingest-gateway services/ingest-gateway
RUN cd services/ingest-gateway && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ingest-gateway ./cmd/ingest-gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ingest-gateway /ingest-gateway
COPY data/reference/wmi_synthetic.csv /ref/wmi_synthetic.csv
ENV GATEWAY_WMI_FILE=/ref/wmi_synthetic.csv GATEWAY_HTTP_ADDR=:8081
USER nonroot:nonroot
EXPOSE 8081
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/ingest-gateway", "healthcheck"]
ENTRYPOINT ["/ingest-gateway"]
