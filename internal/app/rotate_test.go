package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	secretbox "cc-manager/internal/crypto"
)

func TestRotateKeysRewritesLegacyRowsAndRefusesUnknownKeys(t *testing.T) {
	oldKey := bytes.Repeat([]byte{3}, 32)
	newKey := bytes.Repeat([]byte{4}, 32)
	legacy, _ := secretbox.New(oldKey)
	seal := func(plain, aad string) string {
		raw, _ := legacy.Seal([]byte(plain), []byte(aad))
		return base64.StdEncoding.EncodeToString(raw)
	}
	path := t.TempDir() + "/rotate.db"
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "rotation-test-password")
	t.Setenv("CC_MANAGER_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(newKey))
	t.Setenv("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS", "")
	a, err := newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	stamp := now()
	mustExec := func(q string, args ...any) {
		if _, err := a.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO accounts(id,account,credentials_json,created_at,updated_at) VALUES('acc','a@example.test',?,?,?)`, seal(`{"email":"a@example.test"}`, "acc"), stamp, stamp)
	mustExec(`INSERT INTO account_secrets(account_id,password_secret) VALUES('acc',?)`, seal("pw", "acc"))
	mustExec(`INSERT INTO proxies(id,scheme,host,port,username,password_secret,created_at,updated_at) VALUES('px','http','127.0.0.1',8080,'u',?,?,?)`, seal("proxy-pw", "px"), stamp, stamp)
	mustExec(`INSERT INTO platform_connections(id,token_secret,created_at,updated_at) VALUES('conn',?,?,?)`, seal("token", "conn"), stamp, stamp)
	mustExec(`UPDATE proxy_probe_settings SET password_secret=? WHERE id=1`, seal("front", proxySettingsAAD))
	mustExec(`INSERT INTO imports(id,format,content_hash,rows_json,state,created_at,expires_at) VALUES('imp','accounts','h',?,'preview',?,?)`, `[{"account":"x","password_secret":"`+seal("p", "imp:password")+`"}]`, stamp, stamp)

	// Without the previous key the rotation must fail and change nothing.
	if _, err := a.RotateKeys(context.Background(), false); err == nil {
		t.Fatal("rotation succeeded without the old key")
	}
	var token string
	_ = a.DB.QueryRow(`SELECT token_secret FROM platform_connections WHERE id='conn'`).Scan(&token)
	if a.keys.NeedsRotation(token) != true {
		t.Fatal("failed rotation modified rows")
	}
	a.Close()

	t.Setenv("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS", base64.StdEncoding.EncodeToString(oldKey))
	a, err = newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	check, err := a.RotateKeys(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if check.Pending["accounts"] != 1 || check.Pending["imports"] != 1 || check.Pending["proxy_probe_settings"] != 1 || len(check.Rewritten) != 0 {
		t.Fatalf("check report=%+v", check)
	}
	report, err := a.RotateKeys(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rewritten["accounts"] != 1 || report.Rewritten["account_secrets"] != 1 || report.Rewritten["proxies"] != 1 || report.Rewritten["platform_connections"] != 1 || report.Rewritten["proxy_probe_settings"] != 1 || report.Rewritten["imports"] != 1 {
		t.Fatalf("report=%+v", report)
	}
	again, _ := a.RotateKeys(context.Background(), true)
	for table, n := range again.Pending {
		if n != 0 {
			t.Fatalf("%s still pending after rotation", table)
		}
	}
	// Everything stays readable with only the new key configured.
	a.Close()
	t.Setenv("CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS", "")
	a, err = newApp(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if plain, err := a.openSecret(tokenOf(t, a), "conn"); err != nil || string(plain) != "token" {
		t.Fatalf("token unreadable after rotation: %v", err)
	}
	if auth, err := a.loadCredential("acc"); err != nil || auth["email"] != "a@example.test" {
		t.Fatalf("credential unreadable after rotation: %v", err)
	}
	if url, err := a.resolveProxyURL("px", ""); err != nil || url == "" {
		t.Fatalf("proxy unreadable after rotation: %v", err)
	}
}

func tokenOf(t *testing.T, a *App) string {
	t.Helper()
	var token string
	if err := a.DB.QueryRow(`SELECT token_secret FROM platform_connections WHERE id='conn'`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}
