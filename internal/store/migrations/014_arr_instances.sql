-- Which Sonarr/Radarr instances each collection downloads from. New collections start with the
-- default instance of each kind (preferences default_sonarr / default_radarr); the trigger applies
-- that to every way a collection can be created.
CREATE TABLE collection_arr (
 collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL REFERENCES arr_connections(id) ON DELETE CASCADE,
 PRIMARY KEY(collection_id,connection_id)
);
-- Keep today's behaviour: every collection used every connected instance (opt-outs still gate it).
INSERT INTO collection_arr(collection_id,connection_id)
 SELECT c.id,a.id FROM collections c, arr_connections a;
-- The default of each kind is its first instance.
INSERT OR IGNORE INTO preferences(key,value)
 SELECT 'default_sonarr', id FROM arr_connections WHERE kind='sonarr' ORDER BY name,id LIMIT 1;
INSERT OR IGNORE INTO preferences(key,value)
 SELECT 'default_radarr', id FROM arr_connections WHERE kind='radarr' ORDER BY name,id LIMIT 1;
CREATE TRIGGER collections_arr_defaults AFTER INSERT ON collections
BEGIN
 INSERT OR IGNORE INTO collection_arr(collection_id,connection_id)
  SELECT NEW.id, value FROM preferences
  WHERE key IN ('default_sonarr','default_radarr') AND value IN (SELECT id FROM arr_connections);
END;
