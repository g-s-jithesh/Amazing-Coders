-- fleet schema, master data core (3NF). Owned by fleet-api.
-- ponytail: plain SQL for now; the first Alembic revision will execute this file when fleet-api lands (F-10),
-- together with RLS policies and the kilowatt_owner / kilowatt_app roles.
-- Idempotent: safe to run repeatedly.

CREATE TABLE IF NOT EXISTS fleet.tenant (
  id          uuid PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS fleet.fleet (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES fleet.tenant(id),
  name       text NOT NULL,
  city       text NOT NULL,
  UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS fleet.depot (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES fleet.tenant(id),
  fleet_id   uuid NOT NULL REFERENCES fleet.fleet(id),
  name       text NOT NULL,
  city       text NOT NULL,
  lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
  lon        double precision NOT NULL CHECK (lon BETWEEN -180 AND 180)
);
CREATE INDEX IF NOT EXISTS depot_fleet_idx ON fleet.depot (fleet_id);

-- Global catalogue (not tenant-owned).
CREATE TABLE IF NOT EXISTS fleet.vehicle_model (
  code                   text PRIMARY KEY,
  name                   text NOT NULL,
  oem                    text NOT NULL,
  chemistry              text NOT NULL CHECK (chemistry IN ('LFP', 'NMC')),
  nominal_capacity_kwh   numeric(6,2) NOT NULL CHECK (nominal_capacity_kwh > 0),
  nominal_voltage_v      numeric(6,1) NOT NULL CHECK (nominal_voltage_v > 0),
  max_ac_kw              numeric(6,2) NOT NULL CHECK (max_ac_kw > 0),
  max_dc_kw              numeric(6,2) NOT NULL CHECK (max_dc_kw > 0),
  consumption_wh_per_km  integer NOT NULL CHECK (consumption_wh_per_km > 0)
);

CREATE TABLE IF NOT EXISTS fleet.battery_pack (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES fleet.tenant(id),
  pack_serial   text NOT NULL UNIQUE,
  model_code    text NOT NULL REFERENCES fleet.vehicle_model(code),
  install_date  date NOT NULL
);

CREATE TABLE IF NOT EXISTS fleet.vehicle (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES fleet.tenant(id),
  vin              char(17) NOT NULL UNIQUE CHECK (vin ~ '^[A-HJ-NPR-Z0-9]{17}$'),
  fleet_id         uuid NOT NULL REFERENCES fleet.fleet(id),
  model_code       text NOT NULL REFERENCES fleet.vehicle_model(code),
  current_pack_id  uuid UNIQUE REFERENCES fleet.battery_pack(id),
  oem              text NOT NULL CHECK (oem IN ('oem_a', 'oem_b', 'oem_c')),
  home_depot_id    uuid NOT NULL REFERENCES fleet.depot(id),
  commissioned_on  date NOT NULL,
  latest_soh_pct   numeric(5,2) CHECK (latest_soh_pct BETWEEN 0 AND 100) -- denormalised cache, see docs/er/denormalisation.md
);
CREATE INDEX IF NOT EXISTS vehicle_fleet_idx ON fleet.vehicle (fleet_id);
CREATE INDEX IF NOT EXISTS vehicle_depot_idx ON fleet.vehicle (home_depot_id);

-- PII columns (encrypted with per-driver keys) arrive with crypto-shredding (F-11); only the pseudonym exists now.
CREATE TABLE IF NOT EXISTS fleet.driver (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES fleet.tenant(id),
  driver_ref  text NOT NULL UNIQUE,
  depot_id    uuid NOT NULL REFERENCES fleet.depot(id)
);

-- tenant_id/depot_id are NULL for PUBLIC sites.
CREATE TABLE IF NOT EXISTS fleet.charger_site (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid REFERENCES fleet.tenant(id),
  depot_id           uuid UNIQUE REFERENCES fleet.depot(id),
  kind               text NOT NULL CHECK (kind IN ('DEPOT', 'PUBLIC')),
  name               text NOT NULL,
  city               text NOT NULL,
  lat                double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
  lon                double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
  geohash6           char(6) NOT NULL,
  site_power_cap_kw  numeric(8,1) NOT NULL CHECK (site_power_cap_kw > 0),
  CHECK ((kind = 'DEPOT') = (tenant_id IS NOT NULL AND depot_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS charger_site_geohash_idx ON fleet.charger_site (geohash6);

CREATE TABLE IF NOT EXISTS fleet.charger (
  id              uuid PRIMARY KEY,
  tenant_id       uuid REFERENCES fleet.tenant(id),
  site_id         uuid NOT NULL REFERENCES fleet.charger_site(id),
  ocpp_id         text NOT NULL UNIQUE,
  connector_type  text NOT NULL CHECK (connector_type IN ('TYPE2_AC', 'CCS2')),
  max_kw          numeric(6,1) NOT NULL CHECK (max_kw > 0)
);
CREATE INDEX IF NOT EXISTS charger_site_idx ON fleet.charger (site_id);

-- Recurring daily duty; minutes are IST minute-of-day. return < depart means the shift crosses midnight.
CREATE TABLE IF NOT EXISTS fleet.vehicle_duty (
  vehicle_id        uuid PRIMARY KEY REFERENCES fleet.vehicle(id),
  tenant_id         uuid NOT NULL REFERENCES fleet.tenant(id),
  depot_id          uuid NOT NULL REFERENCES fleet.depot(id),
  shift             text NOT NULL,
  depart_min_ist    smallint NOT NULL CHECK (depart_min_ist BETWEEN 0 AND 1439),
  return_min_ist    smallint NOT NULL CHECK (return_min_ist BETWEEN 0 AND 1439),
  planned_km        integer NOT NULL CHECK (planned_km > 0),
  required_soc_pct  smallint NOT NULL CHECK (required_soc_pct BETWEEN 0 AND 100)
);
CREATE INDEX IF NOT EXISTS vehicle_duty_depot_idx ON fleet.vehicle_duty (depot_id);
