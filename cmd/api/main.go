package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/google/uuid"
	"meiling/internal/auth"
	"meiling/internal/config"
	"meiling/internal/db"
	"meiling/internal/ipfs"
	"meiling/internal/metrics"
	"meiling/internal/s3"
	"meiling/internal/security"
	"meiling/internal/store"
	"meiling/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration invalid", "error", err)
		os.Exit(1)
	}

	// Structured JSON logging.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DBURL)
	if err != nil {
		slog.Error("connect db", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Tune connection pool.
	pool.Config().MaxConns = int32(cfg.DBMaxOpenConns)
	pool.Config().MinConns = int32(cfg.DBMaxIdleConns)
	pool.Config().MaxConnLifetime = cfg.DBConnMaxLife

	if err := pool.Ping(ctx); err != nil {
		slog.Error("ping db", "error", err)
		os.Exit(1)
	}

	if err := runMigrations(ctx, pool); err != nil {
		slog.Error("migrations", "error", err)
		os.Exit(1)
	}

	// Optional seed: create demo account only if SEED_ACCOUNT=true
	if os.Getenv("SEED_ACCOUNT") == "true" {
		seed(ctx, pool)
	}

	st, err := newStore(cfg)
	if err != nil {
		slog.Error("create store", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.StoragePath, 0750); err != nil {
		slog.Error("storage dir", "error", err)
		os.Exit(1)
	}

	// Optional IPFS adder: when IPFS_UPLOADS_ENABLED is set, PutObject ingests
	// uploads to a Kubo node and returns the CID in x-amz-meta-cid.
	var ipfsAdd s3.IPFSAddFunc
	if cfg.IPFSUploadsEnabled {
		ipfsClient := ipfs.NewNodeClient(cfg.IPFSEndpoint)
		ipfsAdd = func(ctx context.Context, src io.Reader, name string) (string, int64, error) {
			return ipfsClient.Add(ctx, src, name, ipfs.AddOptions{
				Pin:        true,
				CIDVersion: 1,
				RawLeaves:  true,
				Chunker:    "size-1048576",
				Quieter:    true,
			})
		}
	}

	reg := metrics.NewRegistry()

	s3Handler := s3.S3Handler(pool, st, cfg.StoragePath, ipfsAdd, reg)

	// Register IPFS nodes from DB into the distributor.
	dbNodes, err := db.ListNodes(ctx, pool)
	if err != nil {
		slog.Error("list ipfs nodes", "error", err)
		os.Exit(1)
	}
	dist := ipfs.NewDistributor(pool, reg)
	for _, n := range dbNodes {
		dist.RegisterNode(n.ID, n.APIEndpoint)
	}
	ipfsHandler := ipfs.IPFSHandler(pool, dist, reg)

	// Web UI (cookie-based, separate from SigV4 S3 API).
	uiServer := ui.New(cfg.UISecret, pool, st, reg, cfg.PublicHost)

	// Wrap S3 with SigV4 auth.
	authMW := &auth.Middleware{
		Next: s3Handler,
		GetSecret: func(accessKeyID string) (string, error) {
			k, err := db.GetAccessKey(ctx, pool, accessKeyID)
			if err != nil {
				return "", err
			}
			return k.SecretAccessKey, nil
		},
		GetAccount: func(accessKeyID string) (uuid.UUID, string, error) {
			k, err := db.GetAccessKey(ctx, pool, accessKeyID)
			if err != nil {
				return uuid.Nil, "", err
			}
			acc, err := db.GetAccountByID(ctx, pool, k.AccountID)
			if err != nil {
				return uuid.Nil, "", err
			}
			return acc.ID, acc.Role, nil
		},
		OnUnauth: unauthHandler(),
	}

	// Security middleware around S3.
	secMW := security.NewMiddleware(
		authMW,
		cfg.RateLimitRPS,
		cfg.RateLimitBurst,
		cfg.CORSAllowOrigins,
		cfg.MaxBodyBytes,
		cfg.TLSCertFile != "" && cfg.TLSKeyFile != "",
	)

	mux := http.NewServeMux()
	mux.Handle("/", secMW)
	mux.Handle("/ipfs/", ipfsHandler)
	uiServer.Route(mux)
	mux.Handle("/healthz", healthHandler(pool, cfg.StoragePath))
	if cfg.MetricsEnabled {
		mux.Handle("/metrics", reg.Handler())
	}

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Periodic IPFS reconcile.
	go func() {
		ticker := time.NewTicker(cfg.IPFSReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := dist.Reconcile(ctx); err != nil {
					slog.Error("reconcile", "error", err)
				}
			}
		}
	}()

	go func() {
		slog.Info("server listening", "addr", cfg.ListenAddr)
		var err error
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			err = server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	const schema = `
CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY, email TEXT UNIQUE NOT NULL, role TEXT NOT NULL DEFAULT 'user',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS buckets (
    id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS access_keys (
    id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bucket_id UUID REFERENCES buckets(id) ON DELETE CASCADE,
    access_key_id TEXT UNIQUE NOT NULL, secret_access_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS objects (
    id UUID PRIMARY KEY, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    key TEXT NOT NULL, data_path TEXT NOT NULL, size BIGINT NOT NULL,
    content_type TEXT, etag TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (bucket_id, key)
);
CREATE TABLE IF NOT EXISTS ipfs_nodes (
    id UUID PRIMARY KEY, peer_id TEXT, api_endpoint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'healthy', last_seen TIMESTAMPTZ,
    capacity_used BIGINT NOT NULL DEFAULT 0, capacity_total BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS ipfs_pins (
    id UUID PRIMARY KEY, bucket_id UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    request_id TEXT UNIQUE NOT NULL, cid TEXT NOT NULL, name TEXT,
    status TEXT NOT NULL DEFAULT 'queued', size TEXT NOT NULL DEFAULT '0',
    replica_factor INT NOT NULL DEFAULT 2, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS ipfs_pin_replicas (
    id UUID PRIMARY KEY, pin_id UUID NOT NULL REFERENCES ipfs_pins(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES ipfs_nodes(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (pin_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_objects_bucket ON objects(bucket_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pins_bucket ON ipfs_pins(bucket_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pin_replicas_pin ON ipfs_pin_replicas(pin_id);
CREATE INDEX IF NOT EXISTS idx_ipfs_pin_replicas_node ON ipfs_pin_replicas(node_id);
ALTER TABLE objects ADD COLUMN IF NOT EXISTS cid TEXT;
ALTER TABLE objects ADD COLUMN IF NOT EXISTS storage_backend TEXT NOT NULL DEFAULT 'fs';
ALTER TABLE objects ADD COLUMN IF NOT EXISTS ipfs_status TEXT NOT NULL DEFAULT 'none';
ALTER TABLE objects ADD COLUMN IF NOT EXISTS user_metadata JSONB NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_objects_cid ON objects(cid) WHERE cid IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_objects_ipfs_status ON objects(ipfs_status);
ALTER TABLE ipfs_pins ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'api';
ALTER TABLE ipfs_pins ADD COLUMN IF NOT EXISTS object_id UUID REFERENCES objects(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS ipfs_pins_source_idx ON ipfs_pins(source);
CREATE INDEX IF NOT EXISTS ipfs_pins_object_id_idx ON ipfs_pins(object_id);
CREATE TABLE IF NOT EXISTS multipart_uploads (
    upload_id      TEXT PRIMARY KEY,
    bucket_id      UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    key            TEXT NOT NULL,
    content_type   TEXT,
    user_metadata  JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
`
	_, err := pool.Exec(ctx, schema)
	return err
}

func splitEnv(v, def string) []string {
	if v == "" {
		v = def
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func unauthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>AccessDenied</Code><Message>Access Denied</Message><Resource>%s</Resource><RequestId>demo</RequestId></Error>`,
			r.URL.Path)
	})
}

func seed(ctx context.Context, pool *pgxpool.Pool) {
	var count int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count)
	if count > 0 {
		return
	}
	acc, err := db.CreateAccount(ctx, pool, "demo@example.com", db.RoleAdmin)
	if err != nil {
		slog.Error("seed account", "error", err)
		return
	}
	bucket, err := db.CreateBucket(ctx, pool, acc.ID, "demo-bucket")
	if err != nil {
		slog.Error("seed bucket", "error", err)
		return
	}
	accessKey := os.Getenv("SEED_ACCESS_KEY_ID")
	if accessKey == "" {
		accessKey = "AKIAIOSFODNN7EXAMPLE"
	}
	secretKey := os.Getenv("SEED_SECRET_ACCESS_KEY")
	if secretKey == "" {
		secretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	}
	if _, err := db.CreateAccessKey(ctx, pool, acc.ID, &bucket.ID, accessKey, secretKey); err != nil {
		slog.Error("seed access key", "error", err)
	}

	nodeEndpoints := splitEnv(os.Getenv("IPFS_NODE_ENDPOINTS"), "http://ipfs-node-1:5001,http://ipfs-node-2:5001")
	for _, ep := range nodeEndpoints {
		if _, err := db.CreateNode(ctx, pool, ep, ""); err != nil {
			slog.Error("seed node", "endpoint", ep, "error", err)
		}
	}
	slog.Info("seeded", "account", acc.ID, "bucket", bucket.ID, "nodes", nodeEndpoints)
}

func newStore(cfg *config.Config) (store.ObjectStore, error) {
	switch cfg.StorageBackend {
	case "ipfs":
		return store.NewIPFS(cfg.IPFSEndpoint, cfg.IPFSCacheDir), nil
	case "fs":
		return &store.FS{Root: cfg.StoragePath}, nil
	default:
		return nil, fmt.Errorf("unknown storage backend: %s", cfg.StorageBackend)
	}
}

func healthHandler(pool *pgxpool.Pool, storagePath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		details := map[string]string{}

		if err := pool.Ping(r.Context()); err != nil {
			status = "degraded"
			details["db"] = err.Error()
		} else {
			details["db"] = "ok"
		}
		if err := os.MkdirAll(storagePath, 0750); err != nil {
			status = "degraded"
			details["storage"] = err.Error()
		} else {
			details["storage"] = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		if status == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprintf(w, `{"status":"%s","details":%v}`, status, details)
	})
}
