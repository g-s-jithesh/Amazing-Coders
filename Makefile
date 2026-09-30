# The Makefile is the contract (CLAUDE.md §13). Targets are added as the features behind them land.
COMPOSE := docker compose -f infra/compose/docker-compose.yml
ifneq ($(wildcard .env),)
COMPOSE += --env-file .env
endif
export MSYS_NO_PATHCONV := 1  # Git Bash on Windows: stop /workspace becoming C:/Program Files/Git/workspace
PSQL := $(COMPOSE) exec -T postgres psql -U kilowatt -d kilowatt -v ON_ERROR_STOP=1 -q
SEED ?= 42
VEHICLES ?= 100000
TENANTS ?= 3
MODE ?= mqtt
RATE_HZ ?= 0.1
SPEEDUP ?= 1
NOISE ?= realistic
SIM := cd services/simulator && go run ./cmd/simulator
BUF := docker run --rm -v "$(CURDIR)/libs/proto:/workspace" -w /workspace bufbuild/buf:1.47.2

.PHONY: help up down ps logs seed sim test test-int samples proto-gen proto-lint proto-breaking

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

up: ## build and start the local stack (infra + ingest-gateway)
	$(COMPOSE) up -d --build --wait

down: ## stop the stack and remove volumes
	$(COMPOSE) down -v

ps: ## show stack status
	$(COMPOSE) ps -a

logs: ## follow logs (S=<service> to filter)
	$(COMPOSE) logs -f $(S)

seed: ## generate master data (SEED, VEHICLES, TENANTS) and load it into Postgres
	cd services/simulator && go run ./cmd/simulator seed --seed $(SEED) --vehicles $(VEHICLES) --tenants $(TENANTS) --ref ../../data/reference --out ../../data/seed
	$(PSQL) -f - < services/fleet-api/migrations/0001_fleet_core.sql
	$(PSQL) -1 -f - < infra/compose/seed-load.sql
	$(PSQL) -c "SELECT (SELECT count(*) FROM fleet.vehicle) AS vehicles, (SELECT count(*) FROM fleet.depot) AS depots, (SELECT count(*) FROM fleet.charger) AS chargers"

sim: ## stream telemetry (MODE=mqtt|https|kafka-direct RATE_HZ SPEEDUP NOISE DURATION INJECT=<vin>:<fault>)
	$(SIM) run --seed $(SEED) --vehicles $(VEHICLES) --tenants $(TENANTS) --ref ../../data/reference \
		--mode $(MODE) --rate-hz $(RATE_HZ) --speedup $(SPEEDUP) --noise $(NOISE) \
		--ground-truth-out ../../data/ground_truth $(if $(DURATION),--duration $(DURATION)) $(if $(INJECT),--demo-inject $(INJECT))

test: ## unit tests with coverage (all Go modules)
	cd libs/go-common && go test -cover ./...
	cd services/simulator && go test -cover ./...
	cd services/ingest-gateway && go test -cover ./...

test-int: ## integration tests against the local stack (make up first)
	cd services/simulator && go test -tags integration -count=1 ./...
	cd services/ingest-gateway && go test -tags integration -count=1 ./...

samples: ## regenerate libs/oem-samples golden files from the encoders
	cd services/simulator && go test ./internal/adapters/encoders -run TestGoldenFiles -update

proto-gen: ## regenerate Go code from libs/proto (commit the result)
	$(BUF) generate

proto-lint: ## lint canonical schemas
	$(BUF) lint

proto-breaking: ## fail on backward-incompatible schema changes vs main
	$(BUF) breaking --against "https://github.com/g-s-jithesh/Amazing-Coders.git#branch=main,subdir=libs/proto"
