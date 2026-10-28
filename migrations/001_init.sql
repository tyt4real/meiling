CREATE TABLE IF NOT EXISTS accounts (
    id         UUID PRIMARY KEY,
    email      TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS buckets (
    id         UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS access_keys (
    id              UUID PRIMARY KEY,
    account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bucket_id       UUID REFERENCES buckets(id) ON DELETE CASCADE,
    access_key_id   TEXT UNIQUE NOT NULL,
    secret_access_key TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS objects (
    id           UUID PRIMARY KEY,
    bucket_id    UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    data_path    TEXT NOT NULL,
    size         BIGINT NOT NULL,
    content_type TEXT,
    etag         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (bucket_id, key)
);

CREATE TABLE IF NOT EXISTS ipfs_nodes (
    id             UUID PRIMARY KEY,
    peer_id        TEXT,
    api_endpoint   TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'healthy',
    last_seen      TIMESTAMPTZ,
    capacity_used  BIGINT NOT NULL DEFAULT 0,
    capacity_total BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ipfs_pins (
    id          UUID PRIMARY KEY,
    bucket_id   UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    request_id  TEXT UNIQUE NOT NULL,
    cid         TEXT NOT NULL,
    name        TEXT,
    status      TEXT NOT NULL DEFAULT 'queued',
    size        TEXT NOT NULL DEFAULT '0',
    replica_factor INT NOT NULL DEFAULT 2,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ipfs_pin_replicas (
    id        UUID PRIMARY KEY,
    pin_id    UUID NOT NULL REFERENCES ipfs_pins(id) ON DELETE CASCADE,
    node_id   UUID NOT NULL REFERENCES ipfs_nodes(id) ON DELETE CASCADE,
    status    TEXT NOT NULL DEFAULT 'queued',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (pin_id, node_id)
);

CREATE INDEX IF NOT EXISTS idx_objects_bucket ON objects(bucket_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pins_bucket ON ipfs_pins(bucket_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pin_replicas_pin ON ipfs_pin_replicas(pin_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pin_replicas_node ON ipfs_pin_replicas(node_id);
