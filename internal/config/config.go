// Package config provides environment-variable configuration with startup validation.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration, sourced from environment variables.
type Config struct {
	// Server
	ListenAddr     string
	PublicHost     string
	TLSCertFile    string
	TLSKeyFile     string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxBodyBytes   int64

	// Database
	DBURL          string
	DBMaxOpenConns int
	DBMaxIdleConns int
	DBConnMaxLife  time.Duration

	// Storage
	StoragePath string

	// Auth
	AccessKeyID     string
	SecretAccessKey string

	// IPFS
	IPFSReplicaFactor int
	IPFSReconcileInterval time.Duration

	// Security
	RateLimitRPS     float64
	RateLimitBurst   int
	CORSAllowOrigins []string
	AllowedCIDPrefixes []string

	// Metrics
	MetricsEnabled bool
}

// Load reads configuration from environment variables with sensible defaults.
// It returns an error if any required value is missing or invalid.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:        getEnv("LISTEN_ADDR", ":8080"),
		PublicHost:        getEnv("PUBLIC_HOST", "http://localhost:8080"),
		TLSCertFile:       getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:        getEnv("TLS_KEY_FILE", ""),
		ReadTimeout:       getDuration("READ_TIMEOUT", "30s"),
		WriteTimeout:      getDuration("WRITE_TIMEOUT", "30s"),
		IdleTimeout:       getDuration("IDLE_TIMEOUT", "120s"),
		MaxBodyBytes:      getInt64("MAX_BODY_BYTES", "1073741824"), // 1 GiB
		DBURL:             getEnv("DB_URL", "postgres://s3ipfs:s3ipfs@localhost:5432/s3ipfs?sslmode=disable"),
		DBMaxOpenConns:    getInt("DB_MAX_OPEN_CONNS", "25"),
		DBMaxIdleConns:    getInt("DB_MAX_IDLE_CONNS", "5"),
		DBConnMaxLife:     getDuration("DB_CONN_MAX_LIFE", "30m"),
		StoragePath:       getEnv("STORAGE_PATH", "./data"),
		AccessKeyID:       getEnv("ACCESS_KEY_ID", ""),
		SecretAccessKey:   getEnv("SECRET_ACCESS_KEY", ""),
		IPFSReplicaFactor: getInt("IPFS_REPLICA_FACTOR", "2"),
		IPFSReconcileInterval: getDuration("IPFS_RECONCILE_INTERVAL", "10s"),
		RateLimitRPS:      getFloat("RATE_LIMIT_RPS", "50"),
		RateLimitBurst:    getInt("RATE_LIMIT_BURST", "100"),
		CORSAllowOrigins:  splitEnv("CORS_ALLOW_ORIGINS", "*"),
		AllowedCIDPrefixes: splitEnv("ALLOWED_CID_PREFIXES", "bafy,bae, bag, bah, baj, bak, bal, bam, ban, bao, bap, baq, bar, bas, bat, bau, bav, baw, bax, bay, baz, bbb, bbc, bbd, bbe, bbf, bbg, bbh, bbi, bbj, bbk, bbl, bbm, bbn, bbo, bbp, bbq, bbr, bbs, bbt, bbu, bbv, bbw, bbx, bby, bzb, bzc, bzd, bze, bzf, bzg, bzh, bzi, bzj, bzk, bzl, bzm, bzn, bzo, bzp, bzq, bzr, bzs, bzt, bzu, bzv, bzw, bzx, bzy, bzz"),
		MetricsEnabled:    getBool("METRICS_ENABLED", "true"),
	}

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	return c, nil
}

// Validate checks that all required configuration values are present and sane.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("LISTEN_ADDR is required")
	}
	if c.DBURL == "" {
		return fmt.Errorf("DB_URL is required")
	}
	if c.StoragePath == "" {
		return fmt.Errorf("STORAGE_PATH is required")
	}
	if c.AccessKeyID == "" {
		return fmt.Errorf("ACCESS_KEY_ID is required")
	}
	if c.SecretAccessKey == "" {
		return fmt.Errorf("SECRET_ACCESS_KEY is required")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("MAX_BODY_BYTES must be positive")
	}
	if c.RateLimitRPS <= 0 {
		return fmt.Errorf("RATE_LIMIT_RPS must be positive")
	}
	if c.RateLimitBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_BURST must be positive")
	}
	if c.IPFSReplicaFactor < 1 {
		return fmt.Errorf("IPFS_REPLICA_FACTOR must be >= 1")
	}
	// TLS: cert and key must be provided together.
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must both be set or both unset")
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getBool(key, def string) bool {
	v := strings.ToLower(getEnv(key, def))
	return v == "true" || v == "1" || v == "yes"
}

func getInt(key, def string) int {
	v := getEnv(key, def)
	n, err := strconv.Atoi(v)
	if err != nil {
		n, _ = strconv.Atoi(def)
	}
	return n
}

func getInt64(key, def string) int64 {
	v := getEnv(key, def)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		n, _ = strconv.ParseInt(def, 10, 64)
	}
	return n
}

func getFloat(key, def string) float64 {
	v := getEnv(key, def)
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		n, _ = strconv.ParseFloat(def, 64)
	}
	return n
}

func getDuration(key, def string) time.Duration {
	v := getEnv(key, def)
	d, err := time.ParseDuration(v)
	if err != nil {
		d, _ = time.ParseDuration(def)
	}
	return d
}

func splitEnv(key, def string) []string {
	v := getEnv(key, def)
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
