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
BUF := docker run --rm -v "$(CURDIR)/libs/proto:/workspace" -w /workspace bufbuild/buf:1.47.2

.PHONY: help up down ps logs seed test proto-lint proto-breaking

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

up: ## start the local stack (infra only for now)
	$(COMPOSE) up -d --wait
	$(COMPOSE) wait kafka-init

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

test: ## unit tests with coverage
	cd services/simulator && go test -cover ./...

proto-lint: ## lint canonical schemas
	$(BUF) lint

proto-breaking: ## fail on backward-incompatible schema changes vs main
	$(BUF) breaking --against "https://github.com/g-s-jithesh/Amazing-Coders.git#branch=main,subdir=libs/proto"
