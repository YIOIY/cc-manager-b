package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cc-manager/internal/claudeoauth"
)

// oauthUAPool is the deployment-wide presentation pool. Administrators can
// replace it, but individual accounts keep the profile they already persisted.
var defaultOAuthUAPool = []string{
	"claude-cli/1.0.118 (external, cli)",
	"claude-cli/1.0.119 (external, cli)",
	"claude-cli/1.0.120 (external, cli)",
	"claude-cli/1.0.121 (external, cli)",
	"claude-cli/1.0.122 (external, cli)",
	"claude-cli/1.0.123 (external, cli)",
	"claude-cli/1.0.124 (external, cli)",
	"claude-cli/1.0.125 (external, cli)",
}

var oauthProfileMemory = struct {
	sync.Mutex
	values map[string]claudeoauth.HeaderProfile
}{values: map[string]claudeoauth.HeaderProfile{}}

type oauthFingerprintAnchor struct {
	Email          string `json:"email"`
	AccountID      string `json:"account_id,omitempty"`
	UserAgent      string `json:"user_agent"`
	AcceptLanguage string `json:"accept_language"`
}

func oauthProfileMemoryKey(accountID, email string) string {
	return strings.ToLower(strings.TrimSpace(accountID)) + "|" + strings.ToLower(strings.TrimSpace(email))
}

// deriveOAuthProfile chooses one version for one identity. It is deterministic
// for the same email, so restarts and parallel browser sessions agree.
func deriveOAuthProfile(pool []string, accountID, email string) claudeoauth.HeaderProfile {
	if len(pool) == 0 {
		pool = defaultOAuthUAPool
	}
	seed := strings.ToLower(strings.TrimSpace(email))
	if seed == "" {
		seed = strings.ToLower(strings.TrimSpace(accountID))
	}
	if seed == "" {
		seed = "new-session"
	}
	sum := sha256.Sum256([]byte(seed))
	n := uint64(sum[0]) | uint64(sum[1])<<8 | uint64(sum[2])<<16 | uint64(sum[3])<<24
	index := int(n % uint64(len(pool)))
	return claudeoauth.HeaderProfile{UserAgent: pool[index], AcceptLanguage: "en-US,en;q=0.9"}
}

func encodeOAuthProfile(email, accountID string, profile claudeoauth.HeaderProfile) (string, error) {
	raw, err := json.Marshal(oauthFingerprintAnchor{Email: email, AccountID: accountID, UserAgent: profile.UserAgent, AcceptLanguage: profile.AcceptLanguage})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func loadOAuthProfile(ctx context.Context, a *App, accountID, email string) (claudeoauth.HeaderProfile, bool, error) {
	key := oauthProfileMemoryKey(accountID, email)
	oauthProfileMemory.Lock()
	if profile, ok := oauthProfileMemory.values[key]; ok {
		oauthProfileMemory.Unlock()
		return profile, true, nil
	}
	oauthProfileMemory.Unlock()
	if accountID != "" {
		var raw string
		err := a.DB.QueryRowContext(ctx, `SELECT COALESCE(metadata_json,'{}') FROM accounts WHERE id=?`, accountID).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return claudeoauth.HeaderProfile{}, false, err
		}
		if err == nil {
			var metadata map[string]any
			if err := json.Unmarshal([]byte(raw), &metadata); err == nil {
				if anchor, ok := metadata["_oauth_fp"].(map[string]any); ok {
					if profile, ok := oauthProfileFromAnchor(anchor, email); ok {
						return profile, true, nil
					}
				}
			}
		}
	}
	return deriveOAuthProfile(currentOAuthUAPool(a), accountID, email), false, nil
}

func oauthProfileFromAnchor(anchor map[string]any, email string) (claudeoauth.HeaderProfile, bool) {
	if em, _ := anchor["email"].(string); strings.EqualFold(strings.TrimSpace(em), strings.TrimSpace(email)) {
		ua, _ := anchor["user_agent"].(string)
		lang, _ := anchor["accept_language"].(string)
		if ua != "" {
			if lang == "" {
				lang = "en-US,en;q=0.9"
			}
			return claudeoauth.HeaderProfile{UserAgent: ua, AcceptLanguage: lang}, true
		}
	}
	return claudeoauth.HeaderProfile{}, false
}

// persistOAuthProfile reserves the presentation before the first provider call.
// Like a device anchor, this internal metadata write does not change the public
// account version, and the existing account lease serializes it with workers.
func persistOAuthProfile(ctx context.Context, a *App, accountID, email string, profile claudeoauth.HeaderProfile) error {
	if accountID == "" {
		oauthProfileMemory.Lock()
		oauthProfileMemory.values[oauthProfileMemoryKey(accountID, email)] = profile
		oauthProfileMemory.Unlock()
		return nil
	}
	encoded, err := encodeOAuthProfile(email, accountID, profile)
	if err != nil {
		return err
	}
	result, err := a.DB.ExecContext(ctx, `UPDATE accounts SET metadata_json=json_set(COALESCE(metadata_json,'{}'),'$._oauth_fp',json(?)) WHERE id=?`, encoded, accountID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("oauth fingerprint account missing")
	}
	oauthProfileMemory.Lock()
	oauthProfileMemory.values[oauthProfileMemoryKey(accountID, email)] = profile
	oauthProfileMemory.Unlock()
	return nil
}

// ensureOAuthProfile returns the stable profile for this pending session. The
// lock must already be held by the caller, preventing a race with reauthorization.
func (a *App) persistOAuthProfile(ctx context.Context, accountID, email string, profile claudeoauth.HeaderProfile) {
	_ = persistOAuthProfile(context.WithoutCancel(ctx), a, accountID, email, profile)
}

func (a *App) ensureOAuthProfile(ctx context.Context, p *oauthPending) (claudeoauth.HeaderProfile, error) {
	profile, _, err := loadOAuthProfile(ctx, a, p.accountID, p.email)
	if err != nil {
		return claudeoauth.HeaderProfile{}, err
	}
	if p.email != "" && p.accountID != "" {
		if err := persistOAuthProfile(ctx, a, p.accountID, p.email, profile); err != nil {
			return claudeoauth.HeaderProfile{}, err
		}
	}
	p.fp = &profile
	return profile, nil
}

var oauthUAPoolMu sync.Mutex
var oauthUAPoolCache []string

// currentOAuthUAPool reads the configured pool. A malformed or empty configured
// pool falls back to the built-in versions rather than making requests anonymous.
func currentOAuthUAPool(a *App) []string {
	oauthUAPoolMu.Lock()
	defer oauthUAPoolMu.Unlock()
	if oauthUAPoolCache != nil {
		return oauthUAPoolCache
	}
	s, err := a.loadOAuthDispatchSettings()
	if err == nil {
		if pool, e := parseOAuthUAPool(s.UAPoolJSON); e == nil && len(pool) > 0 {
			oauthUAPoolCache = pool
			return pool
		}
	}
	oauthUAPoolCache = append([]string(nil), defaultOAuthUAPool...)
	return oauthUAPoolCache
}

func resetOAuthUAPoolCache() {
	oauthUAPoolMu.Lock()
	oauthUAPoolCache = nil
	oauthUAPoolMu.Unlock()
}

func parseOAuthUAPool(raw string) ([]string, error) {
	var pool []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &pool); err != nil {
		return nil, err
	}
	for _, ua := range pool {
		if len(strings.TrimSpace(ua)) == 0 || len(ua) > 120 {
			return nil, errors.New("invalid user agent")
		}
	}
	return pool, nil
}
