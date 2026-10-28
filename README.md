# meiling

From-scratch rebuild of Filebase's core: S3-compatible object storage + IPFS pinning service API.

## What it implements

**S3 API** (SigV4, XML responses, region `auto`): buckets, objects, `ListObjectsV2`, `HeadObject`, `GetObject`, `PutObject`, `DeleteObject`, `HeadBucket`, `CreateBucket`, `DeleteBucket`, `GetBucketLocation`, path-style and virtual-host-style addressing.

**IPFS Pinning API** (Bearer token): list/add/replace/delete pins, statuses `queued`/`pinned`, opaque request IDs.

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

See `AGENTS.md` for full developer guidance.
