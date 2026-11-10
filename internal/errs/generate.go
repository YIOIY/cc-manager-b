package errs

import (
	"fmt"
	"sort"
	"strings"
)

//go:generate go run ./gen

func tsQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// GenerateTS renders the registry as frontend constants so codes, actions,
// field labels and fallback messages are never hand-copied.
func GenerateTS() string {
	var b strings.Builder
	b.WriteString("// Generated from internal/errs by `make generate`. DO NOT EDIT.\n\n")
	b.WriteString("export const ErrorCodes = {\n")
	for _, d := range All() {
		fmt.Fprintf(&b, "  %s: %s,\n", d.Code, tsQuote(d.Code))
	}
	b.WriteString("} as const\n\nexport type ErrorCode = keyof typeof ErrorCodes\n\n")
	b.WriteString("export const ErrorCategories = ['auth', 'permission', 'validation', 'not_found', 'conflict', 'rate_limit', 'upstream', 'config', 'storage', 'internal'] as const\n\n")
	b.WriteString("export const ErrorActions = ['none', 'retry', 'wait', 'login', 'fix_input', 'contact_admin', 'check_platform', 'reauthorize'] as const\n\n")
	b.WriteString("/** Fallback copy for records that stored only a code. */\nexport const ErrorMessages: Record<ErrorCode, string> = {\n")
	for _, d := range All() {
		fmt.Fprintf(&b, "  %s: %s,\n", d.Code, tsQuote(d.Message))
	}
	b.WriteString("}\n\n")
	b.WriteString("/** JSON path → user-facing label, same table the backend uses. */\nexport const FieldLabels: Record<string, string> = {\n")
	keys := make([]string, 0, len(FieldLabels))
	for k := range FieldLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s: %s,\n", tsQuote(k), tsQuote(FieldLabels[k]))
	}
	b.WriteString("}\n")
	return b.String()
}

// GenerateDoc renders the registry as the error reference document.
func GenerateDoc() string {
	var b strings.Builder
	b.WriteString("# 错误码参考\n\n本文件由 `make generate` 生成，请勿手改；来源是 `internal/errs/registry.go`。\n\n")
	b.WriteString("| Code | 分类 | HTTP | 可重试 | 动作 | 文案 | 说明 |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, d := range All() {
		status := "—"
		if d.Status != 0 {
			status = fmt.Sprint(d.Status)
		}
		retry := "否"
		if d.Retryable {
			retry = "是"
		}
		doc := d.Doc
		if d.AllowCustomMessage {
			doc = strings.TrimSuffix(doc, "。") + "；允许调用方自定义文案"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s |\n", d.Code, d.Category, status, retry, d.Action, d.Message, strings.TrimPrefix(doc, "—"))
	}
	b.WriteString("\nHTTP 为 — 的条目只出现在异步结果（任务、账单同步、代理检测、预检）或前端本地错误中。\n")
	return b.String()
}
