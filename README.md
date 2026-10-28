# meiling

From-scratch rebuild of Filebase's core: S3-compatible object storage + IPFS pinning service API.

## What it implements

**S3 API** (SigV4, XML responses, region `auto`): buckets, objects, `ListObjectsV2`, `HeadObject`, `GetObject`, `PutObject`, `DeleteObject`, `HeadBucket`, `CreateBucket`, `DeleteBucket`, `GetBucketLocation`, path-style and virtual-host-style addressing.

**HTTP byte-range reads**: `GET /{bucket}/{key}` and `HEAD` support `Range: bytes=start-end`, `bytes=start-`, and `bytes=-suffix`, returning `206 Partial Content` (with `Content-Range`, `Content-Length`, `Accept-Ranges: bytes`) or `416 Range Not Satisfiable` (with `Content-Range: bytes */size`).

**IPFS Pinning API** (Bearer token): list/add/replace/delete pins, statuses `queued`/`pinned`, opaque request IDs.

**Pluggable storage**: filesystem (default) or IPFS-backed via the `internal/store.ObjectStore` interface.

## Run

```
docker compose up
```

Seed credentials (demo account + `demo-bucket`):
- Access key: `AKIAIOSFODNN7EXAMPLE`
- Secret key: `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`

## Test with AWS CLI

```
aws s3 mb s3://demo-bucket --endpoint-url http://localhost:8080
aws s3 cp file.txt s3://demo-bucket/file.txt --endpoint-url http://localhost:8080
aws s3 ls s3://demo-bucket --endpoint-url http://localhost:8080
```

## Storage backend

Set `STORAGE_BACKEND=ipfs` to store object content on IPFS instead of the local filesystem. On `PutObject`, content is streamed to an IPFS node and the CID is recorded. On `GetObject`, content is fetched by CID and cached locally under `IPFS_CACHE_DIR` so range reads can be served without re-fetching.

See `AGENTS.md` for the full configuration table, data layout, and the known IPFS range-read limitation.

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

Point Navidrome's music folder at `/mnt/music`.

See `AGENTS.md` for details and known limitations.

See `AGENTS.md` for full developer guidance.
