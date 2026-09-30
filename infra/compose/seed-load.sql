-- Replaces fleet master data with the CSVs in /seed (mounted from data/seed). Run with psql -1 (one transaction).
TRUNCATE fleet.vehicle_duty, fleet.charger, fleet.charger_site, fleet.driver, fleet.vehicle,
         fleet.battery_pack, fleet.vehicle_model, fleet.depot, fleet.fleet, fleet.tenant;
COPY fleet.tenant        (id, name)                                   FROM '/seed/tenant.csv'        WITH (FORMAT csv, HEADER true);
COPY fleet.fleet         (id, tenant_id, name, city)                  FROM '/seed/fleet.csv'         WITH (FORMAT csv, HEADER true);
COPY fleet.depot         (id, tenant_id, fleet_id, name, city, lat, lon) FROM '/seed/depot.csv'      WITH (FORMAT csv, HEADER true);
COPY fleet.vehicle_model (code, name, oem, chemistry, nominal_capacity_kwh, nominal_voltage_v, max_ac_kw, max_dc_kw, consumption_wh_per_km)
                                                                      FROM '/seed/vehicle_model.csv' WITH (FORMAT csv, HEADER true);
COPY fleet.battery_pack  (id, tenant_id, pack_serial, model_code, install_date) FROM '/seed/battery_pack.csv' WITH (FORMAT csv, HEADER true);
COPY fleet.vehicle       (id, tenant_id, vin, fleet_id, model_code, current_pack_id, oem, home_depot_id, commissioned_on)
                                                                      FROM '/seed/vehicle.csv'       WITH (FORMAT csv, HEADER true);
COPY fleet.driver        (id, tenant_id, driver_ref, depot_id)        FROM '/seed/driver.csv'        WITH (FORMAT csv, HEADER true);
COPY fleet.charger_site  (id, tenant_id, depot_id, kind, name, city, lat, lon, geohash6, site_power_cap_kw)
                                                                      FROM '/seed/charger_site.csv'  WITH (FORMAT csv, HEADER true);
COPY fleet.charger       (id, tenant_id, site_id, ocpp_id, connector_type, max_kw) FROM '/seed/charger.csv' WITH (FORMAT csv, HEADER true);
COPY fleet.vehicle_duty  (vehicle_id, tenant_id, depot_id, shift, depart_min_ist, return_min_ist, planned_km, required_soc_pct)
                                                                      FROM '/seed/vehicle_duty.csv'  WITH (FORMAT csv, HEADER true);
ANALYZE fleet.tenant, fleet.fleet, fleet.depot, fleet.vehicle_model, fleet.battery_pack, fleet.vehicle,
        fleet.driver, fleet.charger_site, fleet.charger, fleet.vehicle_duty;
