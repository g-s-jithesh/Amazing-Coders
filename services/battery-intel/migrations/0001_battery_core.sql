-- battery schema (owned by battery-intel). Idempotent. Applied as the schema owner.
-- ponytail: plain SQL until the Python services share an Alembic setup (fleet-api, Step 8).
--
-- Tenant isolation is enforced by Row-Level Security keyed on current_setting('app.tenant_id'),
-- which the service sets per transaction. The service connects as battery_app (NOT a superuser:
-- superusers bypass RLS).

DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'battery_app') THEN
    CREATE ROLE battery_app LOGIN PASSWORD 'battery-app-dev-only';
  END IF;
END $$;

CREATE SCHEMA IF NOT EXISTS battery;
GRANT USAGE ON SCHEMA battery TO battery_app;

-- Reference: global, read-only for the app.
CREATE TABLE IF NOT EXISTS battery.dtc_code (
  code          text PRIMARY KEY CHECK (code ~ '^[PCBU][0-3][0-9A-F]{3}$'),
  system        text NOT NULL,
  severity      text NOT NULL,
  description   text NOT NULL,
  sim_fault     text,
  runbook_id    text,
  source_status text NOT NULL
);
GRANT SELECT ON battery.dtc_code TO battery_app;

-- One row per accepted charge session; the Kalman-smoothed SoH after applying it.
CREATE TABLE IF NOT EXISTS battery.soh_estimate (
  id           bigserial PRIMARY KEY,
  tenant_id    uuid NOT NULL,
  vin          char(17) NOT NULL,
  pack_id      uuid NOT NULL,
  session_id   uuid NOT NULL,
  as_of        timestamptz NOT NULL,
  soh_pct      numeric(6,3) NOT NULL CHECK (soh_pct BETWEEN 0 AND 150),
  ci_low       numeric(6,3) NOT NULL,
  ci_high      numeric(6,3) NOT NULL,
  obs_soh_pct  numeric(6,3) NOT NULL,
  method       text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (pack_id, session_id)  -- idempotency: a redelivered session is a no-op
);
CREATE INDEX IF NOT EXISTS soh_estimate_pack_asof_idx ON battery.soh_estimate (pack_id, as_of DESC);
CREATE INDEX IF NOT EXISTS soh_estimate_vin_asof_idx ON battery.soh_estimate (vin, as_of DESC);

-- Local-linear-trend Kalman state: level (SoH) + fade rate, with the 2x2 covariance.
CREATE TABLE IF NOT EXISTS battery.pack_kf_state (
  pack_id           uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  soh_pct           double precision NOT NULL,
  rate_pct_per_day  double precision NOT NULL,
  p00               double precision NOT NULL CHECK (p00 >= 0),
  p01               double precision NOT NULL,
  p11               double precision NOT NULL CHECK (p11 >= 0),
  as_of             timestamptz NOT NULL,
  n                 integer NOT NULL CHECK (n >= 1)
);

GRANT SELECT, INSERT ON battery.soh_estimate TO battery_app;
GRANT USAGE ON SEQUENCE battery.soh_estimate_id_seq TO battery_app;
GRANT SELECT, INSERT, UPDATE ON battery.pack_kf_state TO battery_app;

ALTER TABLE battery.soh_estimate ENABLE ROW LEVEL SECURITY;
ALTER TABLE battery.pack_kf_state ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON battery.soh_estimate;
CREATE POLICY tenant_isolation ON battery.soh_estimate
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
DROP POLICY IF EXISTS tenant_isolation ON battery.pack_kf_state;
CREATE POLICY tenant_isolation ON battery.pack_kf_state
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
