package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	os.Setenv("ACCESS_KEY_ID", "testkey")
	os.Setenv("SECRET_ACCESS_KEY", "testsecret")
	defer os.Unsetenv("ACCESS_KEY_ID")
	defer os.Unsetenv("SECRET_ACCESS_KEY")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if c.ListenAddr == "" {
		t.Error("ListenAddr should have default")
	}
	if c.DBURL == "" {
		t.Error("DBURL should have default")
	}
	if c.MaxBodyBytes <= 0 {
		t.Error("MaxBodyBytes should be positive")
	}
	if c.ReadTimeout <= 0 {
		t.Error("ReadTimeout should be positive")
	}
}
func TestLoadMissingRequired(t *testing.T) {
	os.Unsetenv("ACCESS_KEY_ID")
	os.Unsetenv("SECRET_ACCESS_KEY")
	_, err := Load()
	if err == nil {
		t.Error("expected error when required keys missing")
	}
}

func TestTLSMismatch(t *testing.T) {
	os.Setenv("ACCESS_KEY_ID", "k")
	os.Setenv("SECRET_ACCESS_KEY", "s")
	os.Setenv("TLS_CERT_FILE", "/tmp/cert.pem")
	defer os.Unsetenv("ACCESS_KEY_ID")
	defer os.Unsetenv("SECRET_ACCESS_KEY")
	defer os.Unsetenv("TLS_CERT_FILE")
	_, err := Load()
	if err == nil {
		t.Error("expected error when TLS cert/key mismatch")
	}
}

func TestRateLimitPositive(t *testing.T) {
	os.Setenv("ACCESS_KEY_ID", "k")
	os.Setenv("SECRET_ACCESS_KEY", "s")
	os.Setenv("RATE_LIMIT_RPS", "0")
	defer os.Unsetenv("ACCESS_KEY_ID")
	defer os.Unsetenv("SECRET_ACCESS_KEY")
	defer os.Unsetenv("RATE_LIMIT_RPS")
	_, err := Load()
	if err == nil {
		t.Error("expected error when RATE_LIMIT_RPS is zero")
	}
}

func TestGetDuration(t *testing.T) {
	os.Setenv("READ_TIMEOUT", "45s")
	os.Setenv("ACCESS_KEY_ID", "k")
	os.Setenv("SECRET_ACCESS_KEY", "s")
	defer os.Unsetenv("READ_TIMEOUT")
	defer os.Unsetenv("ACCESS_KEY_ID")
	defer os.Unsetenv("SECRET_ACCESS_KEY")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ReadTimeout != 45*time.Second {
		t.Errorf("expected 45s, got %v", c.ReadTimeout)
	}
}
