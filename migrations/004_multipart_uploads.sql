-- 004_multipart_uploads.sql
-- S3 multipart upload support.
--
-- Parts are staged under STORAGE_PATH/.multipart/<uploadId>/<partNumber> and
-- each part's MD5 is kept as its ETag. On CompleteMultipartUpload the parts
-- are validated (order + ETags), concatenated through an io.MultiReader into
-- the same ingest path as a single PUT, and the final ETag is the MD5 of the
-- concatenated part MD5s suffixed with "-N" (as S3 does).
-- A janitor removes uploads older than MULTIPART_TTL (default 24 h) together
-- with their part files.

CREATE TABLE IF NOT EXISTS multipart_uploads (
    id           UUID PRIMARY KEY,
    upload_id    TEXT UNIQUE NOT NULL,
    bucket_id    UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    content_type TEXT,
    user_metadata JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS multipart_parts (
    upload_id   TEXT NOT NULL REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE,
    part_number INT NOT NULL,
    size        BIGINT NOT NULL,
    etag        TEXT NOT NULL,
    PRIMARY KEY (upload_id, part_number)
);

CREATE INDEX IF NOT EXISTS idx_multipart_uploads_bucket ON multipart_uploads(bucket_id);
CREATE INDEX IF NOT EXISTS idx_multipart_parts_upload ON multipart_parts(upload_id);
