-- 002_ipfs_storage.sql
-- Adds IPFS-backed storage support to the objects table.
--
-- The cid column stores the IPFS content identifier for IPFS-backed objects
-- (NULL for filesystem-backed objects). storage_backend records which backend
-- serves the object ("fs" or "ipfs").

ALTER TABLE objects ADD COLUMN IF NOT EXISTS cid TEXT;
ALTER TABLE objects ADD COLUMN IF NOT EXISTS storage_backend TEXT NOT NULL DEFAULT 'fs';
CREATE INDEX IF NOT EXISTS idx_objects_cid ON objects(cid) WHERE cid IS NOT NULL;
