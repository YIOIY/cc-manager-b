package app

import (
	"cc-manager/internal/supplier"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is parsed once at startup. Handlers and workers never read the
// environment directly, so misconfiguration fails fast instead of at request time.
type Config struct {
	DBPath             string
	WebDir             string
	AdminUser          string
	AdminPassword      string   // plaintext fallback; AdminPasswordHash is preferred
	AdminPasswordHash  string   // argon2id PHC string produced by `cc-manager hash-password`
	EncryptionKey      []byte   // nil disables credential writes
	PreviousKeys       [][]byte // older keys still needed to read rows until rotate-key runs
	BillInterval       time.Duration
	BillMaxItems       int
	TrustProxy         bool
	WriteRatePerMinute int // per-user mutating requests; 0 disables
	// StubNetworkProbes skips the live proxy check on save and the proxy→Claude
	// reachability check before an authorization link. Only the browser
	// end-to-end suite sets it: its fixture proxies are not real. Never enable
	// it outside tests, because it turns both safeguards into no-ops.
	StubNetworkProbes bool
	Version           string
}

const (
	defaultDBPath       = "cc-manager.db"
	defaultWebDir       = "web/dist"
	defaultBillMaxItems = 10000
	defaultWriteRate    = 120
	minPasswordLength   = 12
)

// LoadConfig reads CC_MANAGER_* environment variables and validates them.
func LoadConfig() (Config, error) {
	cfg := Config{
		DBPath:             envOr("CC_MANAGER_DB", defaultDBPath),
		WebDir:             envOr("CC_MANAGER_WEB_DIR", defaultWebDir),
		AdminUser:          envOr("CC_MANAGER_ADMIN_USER", "admin"),
		AdminPassword:      os.Getenv("CC_MANAGER_ADMIN_PASSWORD"),
		AdminPasswordHash:  strings.TrimSpace(os.Getenv("CC_MANAGER_ADMIN_PASSWORD_HASH")),
		BillInterval:       time.Second,
		BillMaxItems:       defaultBillMaxItems,
		TrustProxy:         os.Getenv("CC_MANAGER_TRUST_PROXY") == "1",
		WriteRatePerMinute: defaultWriteRate,
		StubNetworkProbes:  os.Getenv("CC_MANAGER_STUB_NETWORK_PROBES") == "1",
	}
	if _, err := supplier.ProxyFromEnv(); err != nil {
		return cfg, fmt.Errorf("CC_MANAGER_SUPPLIER_PROXY: %w", err)
	}
	if cfg.AdminPasswordHash != "" {
		if err := validatePasswordHash(cfg.AdminPasswordHash); err != nil {
			return cfg, fmt.Errorf("CC_MANAGER_ADMIN_PASSWORD_HASH: %w", err)
		}
	} else if len(cfg.AdminPassword) < minPasswordLength {
		return cfg, errors.New("CC_MANAGER_ADMIN_PASSWORD must contain at least 12 characters (or set CC_MANAGER_ADMIN_PASSWORD_HASH)")
	}
	if encoded := os.Getenv("CC_MANAGER_ENCRYPTION_KEY"); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return cfg, errors.New("CC_MANAGER_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
		}
		cfg.EncryptionKey = key
	}
	if list := strings.TrimSpace(os.Getenv("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS")); list != "" {
		if cfg.EncryptionKey == nil {
			return cfg, errors.New("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS requires CC_MANAGER_ENCRYPTION_KEY")
		}
		for _, item := range strings.Split(list, ",") {
			key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(item))
			if err != nil || len(key) != 32 {
				return cfg, errors.New("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS must be comma-separated base64 32-byte keys")
			}
			cfg.PreviousKeys = append(cfg.PreviousKeys, key)
		}
	}
	if value := os.Getenv("CC_MANAGER_BILL_INTERVAL_MS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 30000 {
			return cfg, errors.New("CC_MANAGER_BILL_INTERVAL_MS must be between 1 and 30000")
		}
		cfg.BillInterval = time.Duration(n) * time.Millisecond
	}
	if value := os.Getenv("CC_MANAGER_BILL_MAX_ITEMS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return cfg, errors.New("CC_MANAGER_BILL_MAX_ITEMS must be a positive integer")
		}
		cfg.BillMaxItems = n
	}
	if value := os.Getenv("CC_MANAGER_WRITE_RATE_PER_MINUTE"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 100000 {
			return cfg, errors.New("CC_MANAGER_WRITE_RATE_PER_MINUTE must be between 0 and 100000 (0 disables)")
		}
		cfg.WriteRatePerMinute = n
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
