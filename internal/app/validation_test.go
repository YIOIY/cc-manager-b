package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestOptionalFieldsValidation(t *testing.T) {
	valid := map[string]any{"payment_method": "paypal", "register_device": "Mac", "balance": "100", "recharge_time": "2026-11-04", "warranty_period": "30", "tags": []any{"速刷coding"}, "remark": "test"}
	if err := validOptionalMetadata(valid); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"tags": []any{"慢蒸"}}
	if err := validOptionalMetadata(legacy); err != nil {
		t.Fatal(err)
	}
	if got, ok := legacy["tags"].([]any); !ok || len(got) != 1 || got[0] != "coding" {
		t.Fatalf("legacy tag not rewritten: %#v", legacy["tags"])
	}
	for _, tc := range []map[string]any{
		{"payment_method": "unknown"}, {"register_device": "iPad"}, {"balance": 101}, {"balance": "-1"},
		{"recharge_time": "2026-02-30"}, {"warranty_period": 14}, {"tags": []any{"速刷coding", "coding"}},
	} {
		if err := validOptionalMetadata(tc); err == nil {
			t.Fatalf("accepted invalid optional data: %#v", tc)
		}
	}
}

func TestNormalizeAccountTags(t *testing.T) {
	got := normalizeAccountTags([]string{"慢蒸", "速刷", "coding"})
	if len(got) != 2 || got[0] != "coding" || got[1] != "速刷coding" {
		t.Fatalf("normalizeAccountTags=%v", got)
	}
}

func TestUpstreamAccountTags(t *testing.T) {
	got := upstreamAccountTags([]string{"coding", "速刷coding"})
	if len(got) != 2 || got[0] != "慢蒸" || got[1] != "速刷" {
		t.Fatalf("upstreamAccountTags=%v", got)
	}
	if got := tagsForUpstream([]any{"速刷coding"}); len(got.([]string)) != 1 || got.([]string)[0] != "速刷" {
		t.Fatalf("tagsForUpstream=%#v", got)
	}
}

func TestClaudeAuthValidation(t *testing.T) {
	auth := map[string]any{"id_token": "", "access_token": "a", "refresh_token": "r", "last_refresh": "2026-09-20T00:17:44-07:00", "email": "user@example.com", "account_uuid": "u", "organization_uuid": "o", "organization_name": "name", "claude_device_ids": []any{"device"}, "type": "t", "expired": "2026-10-20T00:17:44-07:00", "proxy_url": "socks5h://user:pwd@1.2.3.4:8080", "verification_status": "v"}
	if problems := validateAuth(auth); len(problems) != 0 {
		t.Fatalf("valid auth rejected: %v", problems)
	}
	auth["claude_device_ids"] = []any{}
	if problems := validateAuth(auth); len(problems) != 0 {
		t.Fatalf("empty device ids should be auto-filled: %v", problems)
	}
	ids, _ := auth["claude_device_ids"].([]any)
	if len(ids) != 1 || strings.TrimSpace(fmt.Sprint(ids[0])) == "" || ids[0] == "device" {
		t.Fatalf("expected generated device id, got %v", auth["claude_device_ids"])
	}
	delete(auth, "claude_device_ids")
	if problems := validateAuth(auth); len(problems) != 0 {
		t.Fatalf("missing device ids should be auto-filled: %v", problems)
	}
	if ids, _ = auth["claude_device_ids"].([]any); len(ids) != 1 {
		t.Fatalf("expected generated device id after delete, got %v", auth["claude_device_ids"])
	}
	auth["claude_device_ids"] = []any{"device"}
	auth["proxy_url"] = "ftp://1.2.3.4"
	auth["expired"] = "yesterday"
	problems := strings.Join(validateAuth(auth), ",")
	if !strings.Contains(problems, "AUTH_PROXY_URL_INVALID") || !strings.Contains(problems, "AUTH_EXPIRED_INVALID") {
		t.Fatalf("missing checks: %s", problems)
	}
}
