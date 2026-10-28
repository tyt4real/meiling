# Security & Operations Guide

## Production Checklist

- [ ] Set `TLS_CERT_FILE` and `TLS_KEY_FILE` for HTTPS (or terminate TLS at a reverse proxy).
- [ ] Rotate `SEED_ACCESS_KEY_ID` / `SEED_SECRET_ACCESS_KEY` from the demo values.
- [ ] Set `SECRET_ACCESS_KEY` to a strong, unique value.
- [ ] Restrict `CORS_ALLOW_ORIGINS` to known origins instead of `*`.
- [ ] Tune `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` to your expected traffic.
- [ ] Set `IPFS_NODE_ENDPOINTS` to real IPFS node APIs (not `localhost:1`).
- [ ] Run PostgreSQL with TLS (`sslmode=require`) in production.
- [ ] Pin the container image to a specific digest/tag, not `latest`.
- [ ] Enable the `/metrics` endpoint and scrape with Prometheus.
- [ ] Mount a PersistentVolume for `/data` (object storage) and PostgreSQL data.
- [ ] Review `SECURITY.md` before exposing to the internet.

## Reverse Proxy (TLS termination)

If terminating TLS at nginx / Cloudflare / ALB, set `PUBLIC_HOST` to the `https://` URL and leave `TLS_CERT_FILE`/`TLS_KEY_FILE` unset. The app will still emit `Strict-Transport-Security` only when TLS is configured at the app layer; enforce HSTS at the proxy instead.

## Backup

Object data lives under `STORAGE_PATH` (filesystem). Metadata lives in PostgreSQL.
Back up both:

```
# PostgreSQL
pg_dump -U s3ipfs -d s3ipfs > meiling-$(date +%F).sql

# Object storage
tar czf meiling-data-$(date +%F).tar.gz -C /path/to/data buckets
```

Restore by reloading the SQL dump and extracting the tarball to `STORAGE_PATH`.

## Logs

Logs are structured JSON on stdout. Stream to your log aggregator (Loki, Elasticsearch, CloudWatch).

## Metrics

`GET /metrics` exposes Prometheus text format:
- `meiling_http_requests_total`
- `meiling_http_request_duration_seconds`
- `meiling_pins_total`
- `meiling_pin_status_total{status="..."}`
- `meiling_objects_total`
- `meiling_buckets_total`
- `meiling_db_connections_open`

## Health

`GET /healthz` returns `{"status":"ok",...}` or HTTP 503 when DB or storage is unavailable. Use for Kubernetes liveness/readiness probes.
