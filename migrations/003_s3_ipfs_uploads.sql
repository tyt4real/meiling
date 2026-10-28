-- 003_s3_ipfs_uploads.sql
-- S3 uploads onto the public IPFS network.
--
-- Tracks the IPFS content identifier and pin status per object so that a
-- successful PutObject returns the CID (x-amz-meta-cid) and the object can be
-- pinned and replicated across the public IPFS network.
-- user_metadata stores client-supplied headers (e.g. x-amz-meta-mtime used by
-- rclone) so they round-trip on HeadObject/GetObject.
-- ipfs_pins.source distinguishes S3-sourced pins (created by ingest) from
-- pins created directly through the pinning API.

ALTER TABLE objects
  ADD COLUMN IF NOT EXISTS cid TEXT,
  ADD COLUMN IF NOT EXISTS ipfs_status TEXT NOT NULL DEFAULT 'none',
  ADD COLUMN IF NOT EXISTS content_type TEXT,
  ADD COLUMN IF NOT EXISTS user_metadata JSONB NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS objects_cid_idx ON objects (cid) WHERE cid IS NOT NULL;
CREATE INDEX IF NOT EXISTS objects_ipfs_status_idx ON objects (ipfs_status);

ALTER TABLE ipfs_pins
  ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'api',
  ADD COLUMN IF NOT EXISTS object_id UUID REFERENCES objects(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS ipfs_pins_source_idx ON ipfs_pins (source);
CREATE INDEX IF NOT EXISTS ipfs_pins_object_id_idx ON ipfs_pins (object_id);
