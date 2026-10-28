# meiling

A from-scratch rebuild of Filebase's core functionality:

- **S3-compatible object storage** (SigV4 auth, buckets, objects, `ListObjectsV2`, pre-signed URLs, path + virtual-host addressing) on `:8080`.
- **IPFS pinning service API** (Bearer token, list/add/replace/delete pins) on `:8080/ipfs/...`, with **multi-node replication**.
- **PostgreSQL** for metadata; local filesystem (Docker volume) for object data.

## Developer commands

```
go build ./...              # build the API binary
go vet ./...                # vet
go test ./... -count=1      # run unit + integration tests
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
- `PUT /{bucket}/{key}` — `PutObject`
- `GET /{bucket}/{key}` — `GetObject`
- `HEAD /{bucket}/{key}` — `HeadObject`
- `DELETE /{bucket}/{key}` — `DeleteObject`

AWS CLI quickstart:
```
aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE
aws configure set aws_secret_access_key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
aws configure set default.region auto
aws s3 mb s3://demo-bucket --endpoint-url http://localhost:8080
aws s3 cp file.txt s3://demo-bucket/file.txt --endpoint-url http://localhost:8080
aws s3 ls s3://demo-bucket --endpoint-url http://localhost:8080
```

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

## Configuration (environment variables)

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
| `STORAGE_PATH` | `./data` | Object storage root |
| `ACCESS_KEY_ID` | (required) | S3 access key ID |
| `SECRET_ACCESS_KEY` | (required) | S3 secret access key |
| `IPFS_REPLICA_FACTOR` | `2` | Pin replica count |
| `IPFS_RECONCILE_INTERVAL` | `10s` | Pin reconcile interval |
| `RATE_LIMIT_RPS` | `50` | Rate limit requests/sec |
| `RATE_LIMIT_BURST` | `100` | Rate limit burst |
| `CORS_ALLOW_ORIGINS` | `*` | Allowed CORS origins |
| `METRICS_ENABLED` | `true` | Enable /metrics |
| `SEED_ACCESS_KEY_ID` | `AKIAIOSFODNN7EXAMPLE` | Demo access key |
| `SEED_SECRET_ACCESS_KEY` | `wJalrX...` | Demo secret key |
| `IPFS_NODE_ENDPOINTS` | `http://ipfs-node-1:5001,...` | Comma-separated node APIs |

## Data layout

```
data/buckets/<bucket>/<key>    # object bytes (filesystem)
postgres                       # accounts, buckets, objects, access_keys,
                               # ipfs_nodes, ipfs_pins, ipfs_pin_replicas
```

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
- `internal/store` — filesystem object store
- `internal/validation` — CID + key + bucket name validation

## Production

- Kubernetes manifests: `deploy/k8s/manifests.yaml`
- CI/CD: `.github/workflows/` (ci.yml, docker.yml, release.yml)
- Security policy: `SECURITY.md`
- Changelog: `CHANGELOG.md`
