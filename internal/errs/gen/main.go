// Command gen writes the generated artifacts derived from the error registry.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cc-manager/internal/errs"
)

func main() {
	root := "."
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		root = filepath.Join(root, "..")
	}
	for file, content := range map[string]string{
		filepath.Join(root, "web", "src", "generated", "error-codes.ts"): errs.GenerateTS(),
		filepath.Join(root, "docs", "errors.md"):                         errs.GenerateDoc(),
	} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", file)
	}
}
