package app

import (
	"context"
	"encoding/json"
	"testing"

	"cc-manager/internal/claudeoauth"
)

func TestOAuthProfileStableAcrossSessionsAndPersisted(t *testing.T) {
	pool := []string{"claude-cli/1.0.120 (external, cli)", "claude-cli/1.0.121 (external, cli)"}
	first := deriveOAuthProfile(pool, "", "alice@example.com")
	second := deriveOAuthProfile(pool, "", "Alice@example.com")
	if first != second {
		t.Fatalf("same email derived different profiles: %#v %#v", first, second)
	}

	fingerprint := &oauthFingerprintTestApp{}
	profile, persisted, err := fingerprint.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted || profile.UserAgent == "" {
		t.Fatalf("unexpected initial profile: persisted=%v profile=%#v", persisted, profile)
	}
	profile2, persisted2, err := fingerprint.reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !persisted2 || profile2 != profile {
		t.Fatalf("profile not stable after persist: persisted=%v profile=%#v want %#v", persisted2, profile2, profile)
	}
}

type oauthFingerprintTestApp struct {
	meta string
}

func (f *oauthFingerprintTestApp) load(ctx context.Context) (claudeoauth.HeaderProfile, bool, error) {
	var metadata map[string]any
	if f.meta != "" {
		if err := json.Unmarshal([]byte(f.meta), &metadata); err != nil {
			return claudeoauth.HeaderProfile{}, false, err
		}
	}
	if anchor, ok := metadata["_oauth_fp"].(map[string]any); ok {
		if profile, ok := oauthProfileFromAnchor(anchor, "alice@example.com"); ok {
			return profile, true, nil
		}
	}
	return deriveOAuthProfile(defaultOAuthUAPool, "acct", "alice@example.com"), false, nil
}

func (f *oauthFingerprintTestApp) reload(ctx context.Context) (claudeoauth.HeaderProfile, bool, error) {
	raw, err := encodeOAuthProfile("alice@example.com", "acct", deriveOAuthProfile(defaultOAuthUAPool, "acct", "alice@example.com"))
	if err != nil {
		return claudeoauth.HeaderProfile{}, false, err
	}
	encoded, _ := json.Marshal(map[string]any{"_oauth_fp": json.RawMessage(raw)})
	f.meta = string(encoded)
	return f.load(ctx)
}
