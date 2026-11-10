package app

import (
	"strings"
	"testing"
)

func TestOptionalFieldsValidation(t *testing.T) {
	valid := map[string]any{"payment_method": "paypal", "register_device": "Mac", "balance": "100", "recharge_time": "2026-11-04", "warranty_period": "30", "tags": []any{"速刷"}, "remark": "test"}
	if err := validOptionalMetadata(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []map[string]any{
		{"payment_method": "unknown"}, {"register_device": "iPad"}, {"balance": 101}, {"balance": "-1"},
		{"recharge_time": "2026-02-30"}, {"warranty_period": 14}, {"tags": []any{"速刷", "慢蒸"}},
	} {
		if err := validOptionalMetadata(tc); err == nil {
			t.Fatalf("accepted invalid optional data: %#v", tc)
		}
	}
}

func TestClaudeAuthValidation(t *testing.T) {
	auth := map[string]any{"id_token": "", "access_token": "a", "refresh_token": "r", "last_refresh": "2026-09-20T00:17:44-07:00", "email": "user@example.com", "account_uuid": "u", "organization_uuid": "o", "organization_name": "name", "claude_device_ids": []any{"device"}, "type": "t", "expired": "2026-10-20T00:17:44-07:00", "proxy_url": "socks5h://user:pwd@1.2.3.4:8080", "verification_status": "v"}
	if problems := validateAuth(auth); len(problems) != 0 {
		t.Fatalf("valid auth rejected: %v", problems)
	}
	auth["proxy_url"] = "ftp://1.2.3.4"
	auth["expired"] = "yesterday"
	problems := strings.Join(validateAuth(auth), ",")
	if !strings.Contains(problems, "AUTH_PROXY_URL_INVALID") || !strings.Contains(problems, "AUTH_EXPIRED_INVALID") {
		t.Fatalf("missing checks: %s", problems)
	}
}
