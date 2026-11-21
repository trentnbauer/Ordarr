-- Automatic downloads become the default for every collection, with a per-collection opt-out
-- (replacing the per-connection mode and per-collection opt-in).
CREATE TABLE collection_no_download (
 collection_id TEXT PRIMARY KEY REFERENCES collections(id) ON DELETE CASCADE
);
-- Existing installs that use Sonarr/Radarr keep their behaviour: a collection that was not opted in stays opted out.
INSERT INTO collection_no_download(collection_id)
 SELECT id FROM collections
 WHERE EXISTS (SELECT 1 FROM arr_connections) AND id NOT IN (SELECT collection_id FROM arr_automation);
-- The global default follows whether any connection had automatic requests on; with no connections it stays unset (on).
INSERT INTO preferences(key,value)
 SELECT 'default_auto_download', CASE WHEN EXISTS (SELECT 1 FROM arr_connections WHERE automatic=1) THEN '1' ELSE '0' END
 WHERE EXISTS (SELECT 1 FROM arr_connections);
DROP TABLE arr_automation;
