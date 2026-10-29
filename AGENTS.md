# meiling

A from-scratch rebuild of Filebase's core functionality:

- **S3-compatible object storage** (SigV4 auth, buckets, objects, `ListObjectsV2`, pre-signed URLs, path + virtual-host addressing) on `:8080`.
- **HTTP byte-range reads** (`Range: bytes=...`) on `GET /{bucket}/{key}` and `HEAD`, returning `206 Partial Content` / `416 Range Not Satisfiable` with `Content-Range` and `Accept-Ranges: bytes`.
- **IPFS pinning service API** (Bearer token, list/add/replace/delete pins) on `:8080/ipfs/...`, with **multi-node replication**.
- **Pluggable object storage**: filesystem (default) or IPFS-backed, via the `internal/store.ObjectStore` interface.
- **PostgreSQL** for metadata; local filesystem (Docker volume) for object data.

## Developer commands

```
go build ./...              # build the API binary
go vet ./...                # vet
go test ./... -count=1      # run unit + integration tests (requires TEST_DB_URL)
go test ./... -coverprofile=coverage.out   # coverage report
docker compose up           # start api + postgres (Docker)
docker compose up -d --build api   # rebuild and start API
docker compose logs -f api   # view API logs
```

## Runtime (Docker)

`docker compose up` builds the API and starts PostgreSQL. The API listens on `http://localhost:8080`.

Endpoints:
- `GET /healthz` — health check (DB + storage)
- `GET /metrics` — Prometheus metrics
- `GET /` / `PUT /{bucket}` / etc. — S3 API (SigV4)
- `/ipfs/pins...` — IPFS pinning API (Bearer token)

The service seeds a demo account on first start:

| Item | Value |
|------|-------|
| Access key ID | `AKIAIOSFODNN7EXAMPLE` |
| Secret access key | `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY` |
| Bucket | `demo-bucket` |
| Region | `auto` |

Override with `SEED_ACCESS_KEY_ID` / `SEED_SECRET_ACCESS_KEY` env vars.

## S3 API (SigV4)

Endpoint: `http://localhost:8080`. Region: `auto`. Supports path-style (`/bucket/key`) and virtual-host-style (`bucket.localhost/key`) addressing.

Operations:
- `GET /` — `ListBuckets`
- `PUT /{bucket}` — `CreateBucket`
- `GET /{bucket}?location` — `GetBucketLocation`
- `GET /{bucket}` — `ListObjectsV2`
- `DELETE /{bucket}` — `DeleteBucket`
- `HEAD /{bucket}` — `HeadBucket`
- `PUT /{bucket}/{key}` — `PutObject` (streamed, body not fully buffered)
- `GET /{bucket}/{key}` — `GetObject` (supports `Range: bytes=...`; `206`/`416`)
- `HEAD /{bucket}/{key}` — `HeadObject` (includes `Accept-Ranges: bytes`)
- `DELETE /{bucket}/{key}` — `DeleteObject`
- `POST /{bucket}/{key}?uploads` — `CreateMultipartUpload`
- `PUT /{bucket}/{key}?partNumber=N&uploadId=X` — `UploadPart`
- `POST /{bucket}/{key}?uploadId=X` — `CompleteMultipartUpload`
- `DELETE /{bucket}/{key}?uploadId=X` — `AbortMultipartUpload`
- `POST /{bucket}/{key}?delete` — `DeleteObjects`
- `PUT /{bucket}/{key}?copy` — `CopyObject`

Byte-range behavior:
- `Range: bytes=start-end` → `206 Partial Content`, `Content-Range: bytes start-end/size`
- `Range: bytes=start-` → `206` from start to EOF
- `Range: bytes=-suffix` → `206` last `suffix` bytes
- Unsatisfiable range → `416 Range Not Satisfiable`, `Content-Range: bytes */size`
- No `Range` header → `200 OK` with full body

AWS CLI quickstart:
```
aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE
aws configure set aws_secret_access_key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
aws configure set default.region auto
aws s3 mb s3://demo-bucket --endpoint-url http://localhost:8080
aws s3 cp file.txt s3://demo-bucket/file.txt --endpoint-url http://localhost:8080
aws s3 ls s3://demo-bucket --endpoint-url http://localhost:8080
```

## IPFS uploads to the public network

Set `STORAGE_BACKEND=fs` (default) and `IPFS_UPLOADS_ENABLED=true` so that `PutObject`
and multipart `CompleteMultipartUpload` stream each object to a Kubo node
(`/api/v0/add`, `pin=true`, `cid-version=1`, `raw-leaves=true`,
`chunker=size-1048576`) and return the CID in the `x-amz-meta-cid` response
header (Filebase-compatible). Pins are replicated to `IPFS_REPLICA_FACTOR`
nodes via the existing distributor. If the IPFS node is unavailable mid-upload,
the local copy is kept, the object row is marked `ipfs_status='pending'`, and no
CID header is returned; the reconciler retries the add later.

When `IPFS_UPLOADS_ENABLED=false` (default), objects are stored on the
filesystem only and no CID is returned.

## IPFS pinning API

Endpoint: `http://localhost:8080/ipfs/...`. Auth: `Authorization: Bearer <access_key_id>`.

- `GET /ipfs/pins` — list pins (`status`, `limit`, `before`, `after`)
- `POST /ipfs/pins` — add pin (`{"cid": "...", "name": "..."}`)
- `GET /ipfs/pins/{requestid}` — get pin status (aggregated from replicas)
- `POST /ipfs/pins/{requestid}` — replace pin
- `DELETE /ipfs/pins/{requestid}` — delete pin (unpins from all nodes)

Pin statuses: `queued` → `pinning` → `pinned`/`failed`.

## Multi-node IPFS

IPFS nodes are registered in the `ipfs_nodes` table (seeded from `IPFS_NODE_ENDPOINTS` env var). When a pin is created, the distributor selects `IPFS_REPLICA_FACTOR` healthy nodes (default 2), biased by free capacity, and fires `pin add` on each. Replica status is tracked in `ipfs_pin_replicas` and aggregated to the pin status.

In production, point `ipfs_nodes.api_endpoint` at real IPFS node HTTP APIs (default port 5001).

## IPFS-backed object storage

Set `STORAGE_BACKEND=ipfs` to store object content on IPFS instead of the local filesystem. On `PutObject`, content is streamed to an IPFS node (`/api/v0/add`) and the CID is recorded in the `objects` table. On `GetObject`, content is fetched by CID (`/api/v0/cat`) and cached locally under `IPFS_CACHE_DIR` so that range reads can be served without re-fetching from the network.

**Important limitation:** the IPFS HTTP API's `/api/v0/cat` does not support byte-range reads. The first read of an uncached IPFS object streams the entire object into the local cache; subsequent reads (including ranges) are served from the cache. Range reads therefore do require a complete local copy on first access for IPFS-backed objects. This is documented, not hidden: the implementation never claims to fetch only the requested bytes from IPFS.

## Web UI

Meiling includes a server-rendered administration and user web UI at `/ui`, styled after
https://lain.rocks/ (dark background, green accents, monospace typography, CRT
scanlines).

- `GET /ui/login` / `POST /ui/login` — cookie-based session login. Reuses the S3
  seed credentials: account email (`demo@example.com`) and the S3 secret access
  key as the "password".
- `GET /ui/` — user dashboard: list buckets and navigate into them.
- `GET /ui/bucket/<name>` — list objects in a bucket and upload new objects.
- `GET /ui/object/<bucket>/<key>` — download/view an object.
- `GET /ui/admin/` — admin dashboard (buckets + IPFS nodes summary).
- `GET /ui/admin/nodes` — register and remove IPFS nodes.
- `GET /ui/admin/pins` — list IPFS pins and their replica status.
- `GET /ui/logout` — end the session.

The UI authenticates via a signed session cookie (`meiling_session`). Set
`UI_SECRET` to a strong random value for production; the default is insecure and
only intended for local development.

| Var | Default | Description |
|-----|---------|-------------|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `PUBLIC_HOST` | `http://localhost:8080` | Public base URL |
| `TLS_CERT_FILE` | `` | TLS certificate path |
| `TLS_KEY_FILE` | `` | TLS private key path |
| `READ_TIMEOUT` | `30s` | HTTP read timeout |
| `WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `IDLE_TIMEOUT` | `120s` | HTTP idle timeout |
| `MAX_BODY_BYTES` | `1073741824` | Max request body (1 GiB) |
| `DB_URL` | `postgres://...` | PostgreSQL connection URL |
| `DB_MAX_OPEN_CONNS` | `25` | DB max open connections |
| `DB_MAX_IDLE_CONNS` | `5` | DB max idle connections |
| `DB_CONN_MAX_LIFE` | `30m` | DB conn max lifetime |
| `STORAGE_PATH` | `./data` | Object storage root (filesystem backend) |
| `STORAGE_BACKEND` | `fs` | `fs` or `ipfs` |
| `IPFSENDPOINT` | `http://localhost:5001` | IPFS node API endpoint (IPFS backend) |
| `IPFS_CACHE_DIR` | `ipfs-cache` | Local cache dir for fetched IPFS objects |
| `ACCESS_KEY_ID` | (required) | S3 access key ID |
| `SECRET_ACCESS_KEY` | (required) | S3 secret access key |
| `IPFS_REPLICA_FACTOR` | `2` | Pin replica count |
| `IPFS_RECONCILE_INTERVAL` | `10s` | Pin reconcile interval |
| `IPFS_UPLOADS_ENABLED` | `false` | When true, PutObject/multipart add objects to a Kubo node and return the CID |
| `UI_SECRET` | `` | Session cookie signing secret for the web UI (set a strong random value) |
| `RATE_LIMIT_RPS` | `50` | Rate limit requests/sec |
| `RATE_LIMIT_BURST` | `100` | Rate limit burst |
| `CORS_ALLOW_ORIGINS` | `*` | Allowed CORS origins |
| `METRICS_ENABLED` | `true` | Enable /metrics |
| `SEED_ACCESS_KEY_ID` | `AKIAIOSFODNN7EXAMPLE` | Demo access key |
| `SEED_SECRET_ACCESS_KEY` | `wJalrX...` | Demo secret key |
| `IPFS_NODE_ENDPOINTS` | `http://ipfs-node-1:5001,...` | Comma-separated node APIs |

## Data layout

```
data/buckets/<bucket>/<key>    # object bytes (filesystem backend)
ipfs-cache/buckets/<bucket>/<key>  # cached IPFS object bytes (IPFS backend)
ipfs-cache/meta/<bucket>/<key>.json # CID sidecar (IPFS backend)
postgres                       # accounts, buckets, objects, access_keys,
                               # ipfs_nodes, ipfs_pins, ipfs_pin_replicas
```

The `objects` table gained columns in migrations `002_ipfs_storage.sql` and
`003_s3_ipfs_uploads.sql`:
- `cid TEXT` — IPFS content identifier (NULL for filesystem-backed objects)
- `storage_backend TEXT NOT NULL DEFAULT 'fs'` — `fs` or `ipfs`
- `ipfs_status TEXT NOT NULL DEFAULT 'none'` — `none` | `pending` | `pinned` | `failed`
- `user_metadata JSONB NOT NULL DEFAULT '{}'` — client-supplied `x-amz-meta-*` headers

The `ipfs_pins` table gained `source TEXT` and `object_id UUID` (migration
`003_s3_ipfs_uploads.sql`). The `multipart_uploads` and `multipart_parts`
tables (migration `004_multipart_uploads.sql`) stage multipart uploads.

## Architecture

- `cmd/api` — application entrypoint
- `internal/auth` — SigV4 request validation
- `internal/config` — env var config with startup validation
- `internal/db` — PostgreSQL queries
- `internal/ipfs` — IPFS pinning API + multi-node distributor + node HTTP client
- `internal/metrics` — stdlib Prometheus metrics registry
- `internal/presign` — pre-signed URL generation
- `internal/s3` — S3-compatible API (buckets, objects, XML responses)
- `internal/security` — rate limiting, CORS, security headers, body limits
- `internal/store` — pluggable object store (`ObjectStore` interface), filesystem + IPFS backends
- `internal/ui` — server-rendered administration and user web UI at `/ui` (lain.rocks aesthetic)
- `internal/validation` — CID + key + bucket name validation

## rclone + Navidrome example

Meiling's S3 API works with rclone and can be mounted as a filesystem for Navidrome. Range requests return correct bytes, so seeking within music files works.

Example rclone remote (`~/.config/rclone/rclone.conf`):

```
[meiling]
type = s3
provider = Other
endpoint = http://localhost:8080
access_key_id = AKIAIOSFODNN7EXAMPLE
secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
region = auto
```

Mount read-only for Navidrome:

```
rclone mount meiling:demo-bucket /mnt/music \
  --vfs-cache-mode off \
  --dir-cache-time 5m \
  --read-only \
  --allow-other
```

Point Navidrome's music folder at `/mnt/music`. Navidrome's scanner will `HEAD`/`GET` files and issue range reads while scanning and streaming, all of which Meiling serves with `206 Partial Content`.

rclone is not a mandatory dependency of the Meiling server.

## Production

- Kubernetes manifests: `deploy/k8s/manifests.yaml`
- CI/CD: `.github/workflows/` (ci.yml, docker.yml, release.yml)
- Security policy: `SECURITY.md`
- Changelog: `CHANGELOG.md`

## Known limitations

- IPFS `/api/v0/cat` has no byte-range support; first read of an IPFS object fetches the whole object into `IPFS_CACHE_DIR`. Subsequent range reads use the cache.
- Object deletion in IPFS mode removes the local cache and sidecar but does not auto-unpin the CID; shared-CID retention must be managed via the IPFS pinning API (never unpin a CID still referenced by another object).
- Multiple ranges (`bytes=0-10,20-30`) fall back to `200 OK` full body (not `multipart/byteranges`).
- No multipart uploads; single PUT per object.
- Tests for the S3 API require a PostgreSQL instance (`TEST_DB_URL`).
