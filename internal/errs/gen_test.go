package errs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedArtifactsAreCurrent regenerates into memory and compares with
// the files in the repository; a mismatch means `make generate` was skipped.
func TestGeneratedArtifactsAreCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	for file, want := range map[string]string{
		filepath.Join(root, "web", "src", "generated", "error-codes.ts"): GenerateTS(),
		filepath.Join(root, "docs", "errors.md"):                         GenerateDoc(),
	} {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s missing: %v (run `make generate`)", file, err)
		}
		if string(got) != want {
			t.Errorf("%s is stale, run `make generate`", file)
		}
	}
}

func TestGeneratedTSContainsRegistryAndLabels(t *testing.T) {
	ts := GenerateTS()
	for _, needle := range []string{"VALIDATION_FAILED: 'VALIDATION_FAILED'", "export const FieldLabels", "'auth_json.refresh_token': '刷新令牌'", "export const ErrorMessages", "PLATFORM_REJECTED: '平台拒绝了本次上号'"} {
		if !strings.Contains(ts, needle) {
			t.Errorf("generated TS lacks %q", needle)
		}
	}
	doc := GenerateDoc()
	if !strings.Contains(doc, "| `VALIDATION_FAILED` |") || !strings.Contains(doc, "由 `make generate` 生成") {
		t.Error("generated doc incomplete")
	}
}
