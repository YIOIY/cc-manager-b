package app

import (
	"cc-manager/internal/errs"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

const (
	defaultBodyLimit = 256 << 10 // generous for forms and credentials, far below import limits
	importBodyLimit  = 5 << 20
)

type ctxKey int

const requestIDKey ctxKey = 1

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

// write emits the success envelope. Error responses must go through fail so
// that every error has a code the frontend can map.
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"data": value}
	if id := w.Header().Get("X-Request-ID"); id != "" {
		body["request_id"] = id
	}
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is the single exit for error responses. Registered errors become
// the classified envelope; anything else is an INTERNAL_ERROR whose cause is
// logged with the request id and never sent to the client.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var e *errs.Error
	if !errors.As(err, &e) {
		e = errs.New(errs.CodeInternalError).WithCause(err)
	}
	if cause := e.Cause(); cause != nil {
		attrs := []any{"request_id", w.Header().Get("X-Request-ID"), "code", e.Code(), "error", cause}
		if r != nil {
			attrs = append(attrs, "method", r.Method, "path", r.URL.Path)
		}
		slog.Error("request failed", attrs...)
	}
	if secs, ok := e.Details()["retry_after_seconds"]; ok {
		w.Header().Set("Retry-After", fmt.Sprint(secs))
	}
	payload := map[string]any{
		"code":      e.Code(),
		"category":  e.Category(),
		"message":   e.Message(),
		"retryable": e.Retryable(),
		"action":    e.Action(),
	}
	if e.Hint() != "" {
		payload["hint"] = e.Hint()
	}
	if len(e.Fields()) > 0 {
		payload["fields"] = e.Fields()
	}
	if len(e.Details()) > 0 {
		payload["details"] = e.Details()
	}
	body := map[string]any{"error": payload}
	if id := w.Header().Get("X-Request-ID"); id != "" {
		body["request_id"] = id
	}
	status := e.Status()
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail is the legacy facade: the code selects the registry entry, which owns
// the status and (unless it opts in to custom copy) the message.
func fail(w http.ResponseWriter, status int, code, message string) {
	if def, ok := errs.Lookup(code); ok && def.Status != 0 && def.Status != status {
		slog.Warn("error status mismatch", "code", code, "called_with", status, "registry", def.Status)
	}
	writeError(w, nil, errs.Adhoc(code, status, message))
}

// serverError hides the underlying error from the client but records it with
// the request id so operators can correlate the 500 with the log line.
func serverError(w http.ResponseWriter, r *http.Request, code, message string, err error) {
	writeError(w, r, errs.Adhoc(code, http.StatusInternalServerError, message).WithCause(err))
}

// dbError maps storage failures to the right status: UNIQUE violations are
// client conflicts, everything else is a storage or internal error. A full
// disk or a locked database must never be reported as "account already exists".
func dbError(w http.ResponseWriter, r *http.Request, err error, conflictCode, conflictMessage, failureMessage string) {
	if isUniqueViolation(err) {
		writeError(w, r, errs.Adhoc(conflictCode, http.StatusConflict, conflictMessage))
		return
	}
	writeError(w, r, storageError(err, failureMessage))
}

// storageError classifies a database failure: transient conditions (busy,
// locked, read-only, full) are STORAGE_UNAVAILABLE and worth a retry, anything
// else is INTERNAL_ERROR. The message says which step failed.
func storageError(err error, message string) *errs.Error {
	code := errs.CodeInternalError
	if isStorageUnavailable(err) {
		code = errs.CodeStorageUnavailable
	}
	e := errs.New(code).WithMessage(message)
	if err != nil {
		e = e.WithCause(err)
	}
	return e
}

// isStorageUnavailable reports sqlite conditions that clear up on their own.
func isStorageUnavailable(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code() & 0xff { // primary result code
	case 5, 6, 8, 10, 13: // SQLITE_BUSY, SQLITE_LOCKED, SQLITE_READONLY, SQLITE_IOERR, SQLITE_FULL
		return true
	}
	return false
}

// secretError reports that a stored secret could not be sealed or opened.
func secretError(kind, message string, cause error) *errs.Error {
	e := errs.New(errs.CodeSecretUnreadable).WithMessage(message).WithDetail("secret", kind)
	if cause != nil {
		e = e.WithCause(cause)
	}
	return e
}

// queryInvalid reports one bad query parameter.
func queryInvalid(field, message string) *errs.Error {
	return errs.New(errs.CodeQueryInvalid).WithFields(errs.FieldError{Field: field, Code: errs.FieldInvalidFormat, Message: message})
}

// fieldErr reports a single field problem as VALIDATION_FAILED.
func fieldErr(field, code, message string) *errs.Error {
	return errs.New(errs.CodeValidationFailed).WithFields(errs.FieldError{Field: field, Code: code, Message: message})
}

func isUniqueViolation(err error) bool {
	var e *sqlite.Error
	if errors.As(err, &e) {
		// SQLITE_CONSTRAINT_UNIQUE (2067) and SQLITE_CONSTRAINT_PRIMARYKEY (1555).
		return e.Code() == 2067 || e.Code() == 1555
	}
	return false
}

// decodeJSON reads at most limit bytes and rejects malformed or oversized
// bodies with a client error; it reports whether decoding succeeded.
func decodeJSON(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	if r.ContentLength > limit {
		fail(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "请求体过大")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(body).Decode(value); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "请求体过大")
			return false
		}
		fail(w, http.StatusBadRequest, "REQUEST_MALFORMED", "请求体格式错误")
		return false
	}
	return true
}

// decodeOptionalJSON tolerates an empty body (treated as {}), for endpoints
// whose parameters are all optional.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	if r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody {
		return true
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	err := json.NewDecoder(body).Decode(value)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		fail(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "请求体过大")
		return false
	}
	fail(w, http.StatusBadRequest, "REQUEST_MALFORMED", "请求体格式错误")
	return false
}

func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	fail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "不支持的请求方法")
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	fail(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
}

// clientIP honours proxy headers only when the deployment says the proxy is
// trusted; otherwise a client could spoof its way around login throttling.
func (a *App) clientIP(r *http.Request) string {
	if a.cfg.TrustProxy {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if first, _, ok := strings.Cut(v, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(v)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return a.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *App) requestHost(r *http.Request) string {
	if a.cfg.TrustProxy {
		if v := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); v != "" {
			return v
		}
	}
	return r.Host
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// observe assigns a request id, logs the outcome and sets baseline security
// headers for every response. API responses are never cacheable.
func (a *App) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			id = newID()[:20]
		}
		w.Header().Set("X-Request-ID", id)
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			h.Set("Cache-Control", "no-store")
		}
		rec := &statusRecorder{ResponseWriter: w}
		started := time.Now()
		a.http.inflight.Add(1)
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		a.http.inflight.Add(-1)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		a.http.observe(rec.status, time.Since(started))
		if (r.URL.Path == "/healthz" || r.URL.Path == "/metrics") && rec.status < 400 {
			return
		}
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "http", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status, "bytes", rec.bytes, "duration_ms", time.Since(started).Milliseconds(), "ip", a.clientIP(r))
	})
}

// spaHandler serves the built frontend. Unknown paths without an extension
// fall back to index.html so deep links survive a refresh; hashed assets are
// immutable; directory listings are never exposed.
func spaHandler(dir string) http.Handler {
	root := os.DirFS(dir)
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend build not found; run `npm run build` in web/ or set CC_MANAGER_WEB_DIR", http.StatusNotFound)
		})
	}
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// index.html is read per request so a frontend rebuild (new asset
		// hashes) takes effect without restarting the server.
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "frontend build not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, http.MethodGet, http.MethodHead)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(clean, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				r.URL.Path = clean
				fileServer.ServeHTTP(w, r)
				return
			}
			if filepath.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index)
		}
	})
}

// apiError carries an HTTP status and code out of a handler step so the
// handler ends in a single fail()/serverError() call. cause is only set for
// storage failures and is what gets logged.
// apiError is a registered error returned up the handler chain.
type apiError = errs.Error

func apiFail(status int, code, message string) *apiError {
	return errs.Adhoc(code, status, message)
}

// dbStep wraps a storage error with the user-facing message for that step.
func dbStep(err error, message string) *apiError { return storageError(err, message) }

// dbStepConflict is dbError in return-value form.
func dbStepConflict(err error, conflictCode, conflictMessage, failureMessage string) *apiError {
	if isUniqueViolation(err) {
		return apiFail(http.StatusConflict, conflictCode, conflictMessage)
	}
	return dbStep(err, failureMessage)
}

// respondError maps an error from a handler step to the response.
func respondError(w http.ResponseWriter, r *http.Request, err error) { writeError(w, r, err) }
