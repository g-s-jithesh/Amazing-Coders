# Build from the repo root: docker build -f services/stream-processor/Dockerfile .
# ponytail: base images pinned by tag; pin by digest in the DevOps hardening step (CLAUDE.md §19).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY libs/go-common libs/go-common
COPY libs/proto libs/proto
COPY services/stream-processor/go.mod services/stream-processor/go.sum services/stream-processor/
RUN cd services/stream-processor && go mod download
COPY services/stream-processor services/stream-processor
RUN cd services/stream-processor && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stream-processor ./cmd/stream-processor

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stream-processor /stream-processor
COPY services/stream-processor/config/rules.yaml /config/rules.yaml
COPY data/reference/dtc_catalogue.csv /ref/dtc_catalogue.csv
ENV SP_RULES_FILE=/config/rules.yaml SP_DTC_CATALOGUE=/ref/dtc_catalogue.csv SP_HTTP_ADDR=:9102
USER nonroot:nonroot
EXPOSE 9102
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/stream-processor", "healthcheck"]
ENTRYPOINT ["/stream-processor"]
