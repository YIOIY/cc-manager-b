package app

import (
	"strings"
	"testing"
)

// A background writer must degrade, never crash the process, when handed a
// code the registry does not know (the exact failure the reviewer found for
// CONNECTION_UNREADABLE before it was registered).
func TestFinishJobNeverPanicsOnUnknownCode(t *testing.T) {
	a, _ := rbacApp(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("finishJob panicked: %v", r)
		}
	}()
	a.finishJob("no-such-job", "failed", "NEVER_REGISTERED_CODE")
}

// safeTick turns a panicking background tick into a logged error.
func TestSafeTickRecovers(t *testing.T) {
	ran := false
	safeTick("test", func() { ran = true; panic("boom") })()
	if !ran {
		t.Fatal("tick body did not run")
	}
}

// billMessage must give readable copy for every code bill sync can store.
func TestBillMessageCoversEveryStoredCode(t *testing.T) {
	for _, code := range []string{"CONNECTION_UNREADABLE", "CONNECTION_CHANGED", "TIMEZONE_INVALID", "WINDOW_EXPIRED", "UPSTREAM_HTTP_401", "UPSTREAM_HTTP_403", "CONNECTION_DISABLED", "CONNECTION_INVALID", "RETRY_EXHAUSTED"} {
		msg := billMessage(code)
		if msg == "" || msg == code || strings.ContainsAny(msg, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") {
			t.Errorf("billMessage(%s)=%q is not user-readable", code, msg)
		}
	}
}
