# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |

## Reporting a Vulnerability

Please report security vulnerabilities by emailing hello@example.com with a
description of the issue, steps to reproduce, and the affected version.

Do not open a public issue for security vulnerabilities.

## Security Features

- **Request authentication**: All S3 API requests require AWS Signature Version 4
  (SigV4). IPFS pinning API requests require a bucket-specific Bearer token.
- **Rate limiting**: A per-process token bucket limits request rate per access
  key to prevent abuse.
- **Transport security**: TLS is supported via `TLS_CERT_FILE`/`TLS_KEY_FILE`.
  When configured, non-TLS requests to non-localhost hosts are rejected.
- **Security headers**: Responses include `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, a restrictive
  `Content-Security-Policy`, and `Strict-Transport-Security` when TLS is active.
- **CORS**: Configurable allowed origins via `CORS_ALLOW_ORIGINS`.
- **Input validation**: Object keys are protected against path traversal
  (`..`, absolute paths, null bytes). Bucket names are validated as lowercase
  DNS-safe names. IPFS CIDs are validated against known multibase prefixes.
- **Request size limits**: `MAX_BODY_BYTES` caps the request body size.
- **Non-root container**: The Docker image runs as the unprivileged `s3ipfs`
  user (UID 1000).
- **Secrets**: Access keys and secrets are provided via environment variables
  only. Never commit them to version control.

## Secrets Management

Rotate access keys regularly. Use Docker secrets, Kubernetes secrets, or a
secret manager in production rather than plain environment variables where
possible.

## Dependencies

Dependencies are kept minimal (Go standard library, `pgx`, `google/uuid`).
Vulnerability scans are run on every Docker build via Trivy and results are
uploaded to the GitHub Security tab.

## Disclosure

We will acknowledge receipt of your report within 48 hours and provide a
remediation timeline within 7 days.
