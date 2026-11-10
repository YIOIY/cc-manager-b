package errs

import (
	"regexp"
	"strings"
	"testing"
)

func mustPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	f()
}

func TestRegistryInvariants(t *testing.T) {
	name := regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)
	seen := map[string]bool{}
	for _, d := range All() {
		if seen[d.Code] {
			t.Fatalf("duplicate %s", d.Code)
		}
		seen[d.Code] = true
		if !name.MatchString(d.Code) {
			t.Fatalf("bad name %s", d.Code)
		}
		if strings.TrimSpace(d.Message) == "" {
			t.Fatalf("%s has no message", d.Code)
		}
		for _, w := range []string{"JSON", "DB", "worker", "token", "payload", "抱歉", "对不起"} {
			if strings.Contains(d.Message, w) || strings.Contains(d.Hint, w) {
				t.Fatalf("%s uses banned word %q", d.Code, w)
			}
		}
		if d.Status != 0 && !statusAllowed(d.Category, d.Status) {
			t.Fatalf("%s: %s cannot use %d", d.Code, d.Category, d.Status)
		}
	}
}

func TestErrorBuilders(t *testing.T) {
	e := New("NOT_FOUND").WithDetail("resource", "account")
	if e.Status() != 404 || e.Details()["resource"] != "account" || e.Code() != "NOT_FOUND" {
		t.Fatal(e)
	}
	mustPanic(t, func() { New("NO_SUCH_CODE") })
	mustPanic(t, func() { New("VALIDATION_FAILED").WithDetail("password", "x") })
	mustPanic(t, func() { New("USER_DISABLED").WithMessage("custom") })
	if New("NOT_FOUND").WithMessage("代理不存在").Message() != "代理不存在" {
		t.Fatal("custom message not applied")
	}
	var f Fields
	f.Required("password")
	f.Invalid("plan_end_time", "套餐结束日期不能早于开始日期")
	v := f.Err()
	if v == nil || v.Code() != "VALIDATION_FAILED" || len(v.Fields()) != 2 || v.Message() != "有 2 项资料不符合要求" {
		t.Fatal(v)
	}
	if v.Fields()[0].Message != "缺少密码" || v.Fields()[0].Code != "REQUIRED" || v.Fields()[1].Code != "INVALID_FORMAT" {
		t.Fatalf("%+v", v.Fields())
	}
	if (&Fields{}).Err() != nil {
		t.Fatal("empty fields must be nil")
	}
	cause := New("INTERNAL_ERROR").WithCause(errSentinel)
	if cause.Unwrap() != errSentinel || cause.Error() != cause.Message() {
		t.Fatal("cause not wrapped")
	}
	got := Sanitize("rejected   Bearer abc.def sk-ant-oat01-" + strings.Repeat("x", 50) + " done " + strings.Repeat("Q", 45))
	if strings.Contains(got, "sk-ant") || strings.Contains(got, "abc.def") || strings.Contains(got, "QQQQ") || !strings.Contains(got, "[已隐藏]") || strings.Contains(got, "  ") {
		t.Fatal(got)
	}
	if len(Sanitize(strings.Repeat("甲 ", 400))) > 240*3 {
		t.Fatal("not truncated")
	}
	if Label("auth_json.refresh_token") != "刷新令牌" || Label("unknown_field") != "unknown_field" {
		t.Fatal("labels")
	}
}
