// Package errs is the single registry of every error the application can
// report: HTTP responses, background job outcomes, bill-sync and proxy-probe
// results. It has no business dependencies so supplier and OAuth clients can
// implement its Upstream interface.
package errs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Category groups codes by what the caller should do about them.
type Category string

const (
	CategoryAuth       Category = "auth"
	CategoryPermission Category = "permission"
	CategoryValidation Category = "validation"
	CategoryNotFound   Category = "not_found"
	CategoryConflict   Category = "conflict"
	CategoryRateLimit  Category = "rate_limit"
	CategoryUpstream   Category = "upstream"
	CategoryConfig     Category = "config"
	CategoryStorage    Category = "storage"
	CategoryInternal   Category = "internal"
)

// Action is the suggested next step shown to the user.
type Action string

const (
	ActionNone          Action = "none"
	ActionRetry         Action = "retry"
	ActionWait          Action = "wait"
	ActionLogin         Action = "login"
	ActionFixInput      Action = "fix_input"
	ActionContactAdmin  Action = "contact_admin"
	ActionCheckPlatform Action = "check_platform"
	ActionReauthorize   Action = "reauthorize"
)

// Def is one registry entry. Status is 0 for codes that only appear in
// asynchronous results (job outcomes, probe records, preflight checks).
type Def struct {
	Code               string
	Category           Category
	Status             int
	Retryable          bool
	Action             Action
	Message            string
	Hint               string
	Doc                string
	AllowCustomMessage bool
}

// FieldError points at one input that failed validation.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Field error codes.
const (
	FieldRequired         = "REQUIRED"
	FieldInvalidFormat    = "INVALID_FORMAT"
	FieldOutOfRange       = "OUT_OF_RANGE"
	FieldUnsupportedValue = "UNSUPPORTED_VALUE"
	FieldMismatch         = "MISMATCH"
)

var allowedDetailKeys = map[string]bool{
	"resource": true, "permission": true, "secret": true, "retry_after_seconds": true,
	"upstream_status": true, "job_id": true, "stage": true, "field": true, "next_attempt_at": true,
	"oauth_session_id": true, "oauth_session_expired": true, "oauth_stage": true, "oauth_status": true, "oauth_ms": true, "attempt": true,
	"saved": true, "incoming": true, "proxy_id": true, "account_id": true, "oauth_busy_reason": true,
}

// Error is a registered error with optional per-occurrence data.
type Error struct {
	def     Def
	message string
	hint    string
	fields  []FieldError
	details map[string]any
	cause   error
}

// New builds an error for a registered code. An unknown code is a
// programming error and panics so it is caught by tests, never in production.
func New(code string) *Error {
	def, ok := registry[code]
	if !ok {
		panic(fmt.Sprintf("errs: unregistered code %q", code))
	}
	return &Error{def: def, message: def.Message, hint: def.Hint}
}

// Adhoc wraps a code that is not registered. It exists so a missed call site
// degrades to a classified 500 instead of a panic in production; the registry
// coverage test keeps it from being reached.
func Adhoc(code string, status int, message string) *Error {
	if def, ok := registry[code]; ok {
		// Async-only codes carry no status; an HTTP caller supplies it.
		if def.Status == 0 && status != 0 {
			def.Status = status
		}
		e := &Error{def: def, message: def.Message, hint: def.Hint}
		if def.AllowCustomMessage && strings.TrimSpace(message) != "" {
			e.message = message
		}
		return e
	}
	if status == 0 {
		status = 500
	}
	if strings.TrimSpace(message) == "" {
		message = "服务处理请求时出错"
	}
	return &Error{def: Def{Code: code, Category: CategoryInternal, Status: status, Action: ActionRetry, Retryable: true, Message: message}, message: message}
}

// Lookup returns the definition for a code.
func Lookup(code string) (Def, bool) {
	def, ok := registry[code]
	return def, ok
}

// All returns every definition sorted by code.
func All() []Def {
	out := make([]Def, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// WithMessage overrides the user-facing message; only codes that opt in may
// carry caller-supplied copy, so the registry stays the source of wording.
func (e *Error) WithMessage(msg string) *Error {
	if !e.def.AllowCustomMessage {
		panic(fmt.Sprintf("errs: %s does not allow a custom message", e.def.Code))
	}
	if strings.TrimSpace(msg) != "" {
		e.message = msg
	}
	return e
}

// WithHint overrides the suggested next step.
func (e *Error) WithHint(hint string) *Error { e.hint = hint; return e }

// WithFields attaches field-level problems.
func (e *Error) WithFields(f ...FieldError) *Error { e.fields = append(e.fields, f...); return e }

// WithDetail attaches a whitelisted structured detail.
func (e *Error) WithDetail(key string, value any) *Error {
	if !allowedDetailKeys[key] {
		panic(fmt.Sprintf("errs: detail key %q is not allowed", key))
	}
	if e.details == nil {
		e.details = map[string]any{}
	}
	e.details[key] = value
	return e
}

// WithCause records the underlying error for logs; it is never sent to clients.
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }

func (e *Error) Error() string           { return e.message }
func (e *Error) Unwrap() error           { return e.cause }
func (e *Error) Code() string            { return e.def.Code }
func (e *Error) Status() int             { return e.def.Status }
func (e *Error) Def() Def                { return e.def }
func (e *Error) Message() string         { return e.message }
func (e *Error) Hint() string            { return e.hint }
func (e *Error) Fields() []FieldError    { return e.fields }
func (e *Error) Details() map[string]any { return e.details }
func (e *Error) Cause() error            { return e.cause }
func (e *Error) Retryable() bool         { return e.def.Retryable }
func (e *Error) Action() Action          { return e.def.Action }
func (e *Error) Category() Category      { return e.def.Category }

// Upstream is implemented by client errors from external services so the
// application can classify them without parsing their text.
type Upstream interface {
	error
	UpstreamCode() string
	UpstreamStatus() int
	UpstreamSummary() string
}

var (
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`),
		regexp.MustCompile(`[A-Za-z0-9+/=_-]{40,}`),
	}
)

// Sanitize makes upstream text safe to show: collapses whitespace, caps the
// length and hides anything that looks like a credential.
func Sanitize(summary string) string {
	s := strings.Join(strings.Fields(summary), " ")
	for _, p := range secretPatterns {
		s = p.ReplaceAllString(s, "[已隐藏]")
	}
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240])
	}
	return s
}

func statusAllowed(c Category, status int) bool {
	allowed := map[Category][]int{
		CategoryAuth: {401, 403}, CategoryPermission: {403}, CategoryValidation: {400, 405, 413, 422},
		CategoryNotFound: {404}, CategoryConflict: {409, 422}, CategoryRateLimit: {429},
		// 401/403 reach us as upstream answers too: Claude answers a rejected
		// identity on its own token/profile endpoints and we only relay it.
		CategoryUpstream: {401, 403, 502, 504}, CategoryConfig: {503}, CategoryStorage: {503}, CategoryInternal: {500},
	}
	for _, s := range allowed[c] {
		if s == status {
			return true
		}
	}
	return false
}
