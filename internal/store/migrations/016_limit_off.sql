-- A request limit can be switched off per Sonarr/Radarr instance (daily_limit itself is constrained to 1-100).
ALTER TABLE arr_connections ADD COLUMN limit_off INTEGER NOT NULL DEFAULT 0;
