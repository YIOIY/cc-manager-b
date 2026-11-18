package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	autoStubProxyProbe = true
	os.Exit(m.Run())
}
