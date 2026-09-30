-- Hot raw telemetry (root §5.1: AP, write CL=ONE). Owned by stream-processor. Idempotent: safe to re-run.
-- Dev keyspace uses SimpleStrategy RF 1; the K8s deployment uses NetworkTopologyStrategy RF 3 (Helm values).
CREATE KEYSPACE IF NOT EXISTS kilowatt
  WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

-- Partition (vin, day) bounds partition size (~8.6K rows/day at 0.1 Hz, 86K at 1 Hz) and spreads load;
-- the clustering key makes re-inserts of the same (ts, seq) upserts (at-least-once → effectively once).
-- Absent sensor groups are left unset (never 0, and never bound as null, which would write a tombstone per cell).
CREATE TABLE IF NOT EXISTS kilowatt.telemetry_raw (
  vin text,
  day date,
  ts_event_ms bigint,
  seq bigint,
  ts_ingest_ms bigint,
  event_id text,
  lat double,
  lon double,
  speed_kmh float,
  odo_km double,
  soc_pct float,
  pack_voltage_v float,
  pack_current_a float,
  pack_temp_min_c float,
  pack_temp_max_c float,
  cell_v_min_mv int,
  cell_v_max_mv int,
  isolation_kohm float,
  hv_interlock_ok boolean,
  aux_12v_v float,
  ambient_c float,
  charge_state int,
  charge_power_kw float,
  dtc frozen<list<text>>, -- frozen: a non-frozen collection writes a range tombstone on every INSERT
  evt int,
  PRIMARY KEY ((vin, day), ts_event_ms, seq)
) WITH CLUSTERING ORDER BY (ts_event_ms DESC, seq DESC)
  AND default_time_to_live = 604800
  AND compaction = {'class': 'TimeWindowCompactionStrategy', 'compaction_window_unit': 'HOURS', 'compaction_window_size': 6};
