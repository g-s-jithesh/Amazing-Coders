-- dispatch schema (owned by dispatch-optimizer). Idempotent. Applied as the schema owner.
-- The service connects as dispatch_app (NOT a superuser: superusers bypass RLS). Tenant isolation is
-- Row-Level Security keyed on current_setting('app.tenant_id'), set per transaction.

DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dispatch_app') THEN
    CREATE ROLE dispatch_app LOGIN PASSWORD 'dispatch-app-dev-only';
  END IF;
  -- The outbox relay publishes for every tenant, so it is its own role that can touch only the outbox.
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dispatch_relay') THEN
    CREATE ROLE dispatch_relay LOGIN PASSWORD 'dispatch-relay-dev-only';
  END IF;
END $$;

CREATE SCHEMA IF NOT EXISTS dispatch;
GRANT USAGE ON SCHEMA dispatch TO dispatch_app, dispatch_relay;

-- Reference tariffs: global, read-only for the app (loaded from data/reference/tariffs.csv).
CREATE TABLE IF NOT EXISTS dispatch.tariff_window (
  tariff_code          text NOT NULL,
  dow                  smallint CHECK (dow BETWEEN 0 AND 6),  -- NULL = every day; IST
  start_min            smallint NOT NULL CHECK (start_min BETWEEN 0 AND 1439),
  end_min              smallint NOT NULL CHECK (end_min BETWEEN 0 AND 1440),
  price_paise_per_kwh  integer NOT NULL CHECK (price_paise_per_kwh >= 0),
  source_status        text NOT NULL
);
GRANT SELECT ON dispatch.tariff_window TO dispatch_app;

CREATE TABLE IF NOT EXISTS dispatch.dispatch_plan (
  id                       uuid PRIMARY KEY,
  tenant_id                uuid NOT NULL,
  depot_id                 uuid NOT NULL,
  version                  integer NOT NULL CHECK (version >= 1),
  status                   text NOT NULL CHECK (status IN ('DRAFT', 'APPROVED', 'PUBLISHED', 'SUPERSEDED')),
  method                   text NOT NULL,
  tariff_code              text NOT NULL,
  horizon_start            timestamptz NOT NULL,
  horizon_slots            integer NOT NULL,
  cost_paise               bigint NOT NULL,
  energy_cost_paise        bigint NOT NULL,
  degradation_cost_paise   bigint NOT NULL,
  baseline_cost_paise      bigint NOT NULL,
  baseline_energy_cost_paise      bigint NOT NULL,
  baseline_degradation_cost_paise bigint NOT NULL,
  missed_departures        integer NOT NULL,
  solver_stats             jsonb NOT NULL DEFAULT '{}',
  created_by               text NOT NULL,
  created_at               timestamptz NOT NULL DEFAULT now(),
  approved_by              text,
  approved_at              timestamptz,
  idempotency_key          text,
  UNIQUE (depot_id, version),
  UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS dispatch_plan_depot_idx ON dispatch.dispatch_plan (depot_id, version DESC);

CREATE TABLE IF NOT EXISTS dispatch.dispatch_assignment (
  plan_id          uuid NOT NULL REFERENCES dispatch.dispatch_plan(id) ON DELETE CASCADE,
  tenant_id        uuid NOT NULL,
  vehicle_id       uuid NOT NULL,
  connector_index  integer NOT NULL CHECK (connector_index >= 0),
  slot_start       timestamptz NOT NULL,
  slot_end         timestamptz NOT NULL CHECK (slot_end > slot_start),
  power_kw         numeric(7,3) NOT NULL CHECK (power_kw > 0),
  energy_kwh       numeric(9,3) NOT NULL,
  cost_paise       bigint NOT NULL,
  PRIMARY KEY (plan_id, connector_index, slot_start)  -- no connector is double-booked within a plan
);
CREATE INDEX IF NOT EXISTS dispatch_assignment_vehicle_idx ON dispatch.dispatch_assignment (vehicle_id, slot_start);

-- Transactional outbox: written in the approval transaction, published by the relay (at-least-once).
CREATE TABLE IF NOT EXISTS dispatch.outbox (
  id            bigserial PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  topic         text NOT NULL,
  key           text NOT NULL,
  payload       bytea NOT NULL,
  plan_id       uuid NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  published_at  timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON dispatch.outbox (id) WHERE published_at IS NULL;

GRANT SELECT, INSERT, UPDATE ON dispatch.dispatch_plan TO dispatch_app;
GRANT SELECT, INSERT ON dispatch.dispatch_assignment TO dispatch_app;
GRANT SELECT, INSERT, UPDATE ON dispatch.outbox TO dispatch_app;
GRANT USAGE ON SEQUENCE dispatch.outbox_id_seq TO dispatch_app;
GRANT SELECT, UPDATE (published_at) ON dispatch.outbox TO dispatch_relay;
GRANT SELECT, UPDATE (status) ON dispatch.dispatch_plan TO dispatch_relay;  -- APPROVED → PUBLISHED only

ALTER TABLE dispatch.dispatch_plan ENABLE ROW LEVEL SECURITY;
ALTER TABLE dispatch.dispatch_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE dispatch.outbox ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON dispatch.dispatch_plan;
CREATE POLICY tenant_isolation ON dispatch.dispatch_plan TO dispatch_app
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
DROP POLICY IF EXISTS tenant_isolation ON dispatch.dispatch_assignment;
CREATE POLICY tenant_isolation ON dispatch.dispatch_assignment
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
DROP POLICY IF EXISTS tenant_isolation ON dispatch.outbox;
CREATE POLICY tenant_isolation ON dispatch.outbox TO dispatch_app
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
DROP POLICY IF EXISTS relay_publish ON dispatch.dispatch_plan;
-- USING must also admit the updated row (PostgreSQL re-checks it against the SELECT policy).
CREATE POLICY relay_publish ON dispatch.dispatch_plan TO dispatch_relay
  USING (status IN ('APPROVED', 'PUBLISHED')) WITH CHECK (status = 'PUBLISHED');
DROP POLICY IF EXISTS relay_all ON dispatch.outbox;
CREATE POLICY relay_all ON dispatch.outbox TO dispatch_relay USING (true) WITH CHECK (true);
