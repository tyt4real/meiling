# Changelog

All notable changes to meiling are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [1.0.0] - 2026-10-27

### Added
- S3-compatible object storage API (SigV4 auth, region `auto`)
  - `ListBuckets`, `CreateBucket`, `HeadBucket`, `DeleteBucket`, `GetBucketLocation`
  - `PutObject`, `GetObject`, `HeadObject`, `DeleteObject`, `ListObjectsV2`
  - Path-style and virtual-host-style bucket addressing
  - XML responses matching the AWS S3 schema
- IPFS Pinning Service API (Bearer token auth)
  - List/add/replace/delete pins
  - Statuses: queued, pinning, pinned, failed
  - Opaque request IDs, paginated listing
- Multi-node IPFS pin replication
  - Node registry with health and capacity tracking
  - Replica-factor-based distribution biased by free capacity
  - Per-pin replica status tracking and aggregation
  - Background reconcile loop
- Security hardening
  - AWS Signature Version 4 request authentication
  - Per-access-key rate limiting (token bucket)
  - Security headers (HSTS, X-Content-Type-Options, X-Frame-Options, CSP)
  - CORS with configurable allowed origins
  - Request body size limits
  - CID validation (multibase/multicodec prefixes)
  - Object key path-traversal protection
- Production readiness
  - Health check endpoint (`/healthz`)
  - Prometheus metrics endpoint (`/metrics`)
  - Structured JSON logging
  - Startup configuration validation (fail fast)
  - Tunable database connection pool
  - Graceful shutdown with timeout
  - Graceful shutdown with timeout
  - Non-root container user (`s3ipfs`)
  - Resource limits in Docker Compose
  - Multi-arch Docker builds (linux/amd64, linux/arm64)
- Developer experience
  - Environment-variable configuration
  - Seed demo account, bucket, and keys on first start
  - Pre-signed URL generation (`internal/presign`)
- Testing
  - Unit tests: auth, config, metrics, presign, security, validation
  - Integration tests: full S3 flow and IPFS pin flow against real Postgres
- CI/CD (GitHub Actions)
  - `ci.yml`: Go vet, golangci-lint, tests with coverage on push/PR
  - `docker.yml`: multi-arch image build, Trivy vulnerability scan, GHCR publish
  - `release.yml`: GitHub release creation from version tags

[1.0.0]: https://github.com/meiling/meiling/releases/tag/v1.0.0
