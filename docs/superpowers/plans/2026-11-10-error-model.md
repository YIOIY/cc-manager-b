# 错误分类与错误信息统一 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让后端全部同步 / 异步错误来自一张注册表，响应带分类、字段级错误和 request_id，前端用同一套解析层展示并定位到字段。

**Architecture:** 新包 `internal/errs`（无业务依赖）持有注册表、错误类型与 `Upstream` 接口；`internal/app/httpx.go` 的现有辅助函数改为其门面，信封增量扩展；worker / bill_sync / proxy probe 的结果码与 supplier / claudeoauth 的上游错误都经注册表产出；`go generate` 从注册表生成前端常量与 `docs/errors.md`；前端 `errors.ts` + `ErrorNotice.vue` + `applyFieldErrors` 替换 15 处手写错误展示。四个阶段各自可发布。

**Tech Stack:** Go 1.22（`net/http` 路由、`database/sql` + modernc sqlite、`go/ast` 守卫测试、`go generate`）、Vue 3 + TS + Element Plus、Vitest、Playwright。

**Spec:** `docs/superpowers/specs/2026-11-10-error-model-design.md`

## Global Constraints

- 仓库尚无基线提交（用户待审），本计划不执行 `git commit`；任务以测试通过为完成。
- 成功响应 `{"data":...}` 形状不变，只在响应体增加 `request_id`。现有 `error.code` / `error.message` 字段位置与含义不变。
- 校验采用**全部收集**：一个请求的所有字段问题一次返回在 `fields[]`。
- code 为 `SCREAMING_SNAKE`，命名 `主体_问题`；文案中文、陈述句、禁用词：`JSON`、`DB`、`worker`、`token`、`payload`、`抱歉`、`对不起`。
- 颜色与组件沿用 `web/src/styles/base.css` token；不新增依赖。
- 命令：Go 在仓库根 `go test -race -count=1 ./cmd/... ./internal/...`、`go vet ./...`、`gofmt -l`；前端在 `web/` 下 `npm test -- --reporter=dot`、`npm run lint`、`npm run format:check`、`npm run build`、`npx playwright test`。
- Bash 批量调用一律用绝对路径；BSD sed 无 `\b`，用 perl。

## Review Focus

1. 旧任务的 `result_json` 只有 `{"code":"X"}`（无 message）时，账号列表与任务页仍要显示可读文案（Task 8 的读取派生测试覆盖）。
2. 幂等重放（`Idempotency-Key` 命中）返回的是首次结果，不能因新信封把 202 改成错误（Task 3 的 `respondError` 测试加重放断言）。
3. `PERMISSION_DENIED` 的 `details.permission` 不能泄露不存在的权限名以外的信息；`details` 白名单外的键必须被拒绝（Task 1 单测）。
4. 429 的 `Retry-After` 头与 `details.retry_after_seconds` 必须一致（Task 6 测试）。
5. 上游摘要不能含形如 `sk-ant-…`、`Bearer …`、40+ 位 base64 的片段（Task 8 的 `Summary()` 测试）。

---

## 阶段 1：建表不换血

### Task 1: `internal/errs` 包骨架与守卫单测

**Files:**
- Create: `internal/errs/errs.go`、`internal/errs/registry.go`、`internal/errs/fields.go`
- Test: `internal/errs/errs_test.go`

**Interfaces (Produces):**
```go
package errs
type Category string // CategoryAuth, CategoryPermission, CategoryValidation, CategoryNotFound, CategoryConflict, CategoryRateLimit, CategoryUpstream, CategoryConfig, CategoryStorage, CategoryInternal
type Action string   // ActionNone, ActionRetry, ActionWait, ActionLogin, ActionFixInput, ActionContactAdmin, ActionCheckPlatform, ActionReauthorize
type Def struct { Code string; Category Category; Status int; Retryable bool; Action Action; Message, Hint, Doc string; AllowCustomMessage bool }
type FieldError struct { Field, Code, Message string } // Code ∈ REQUIRED, INVALID_FORMAT, OUT_OF_RANGE, UNSUPPORTED_VALUE, MISMATCH
type Error struct { def Def; message, hint string; fields []FieldError; details map[string]any; cause error }
func New(code string) *Error                       // panics on unknown code (programming error)
func (e *Error) WithMessage(msg string) *Error     // panics unless def.AllowCustomMessage
func (e *Error) WithHint(hint string) *Error
func (e *Error) WithFields(f ...FieldError) *Error
func (e *Error) WithDetail(key string, value any) *Error // panics unless key ∈ allowedDetailKeys
func (e *Error) WithCause(err error) *Error
func (e *Error) Error() string; Unwrap() error; Code() string; Status() int; Def() Def; Fields() []FieldError; Details() map[string]any; Message() string; Hint() string
func Lookup(code string) (Def, bool)
func All() []Def                                   // sorted by Code
var allowedDetailKeys = {"resource","permission","secret","retry_after_seconds","upstream_status","job_id","stage","field"}
type Upstream interface { error; UpstreamCode() string; UpstreamStatus() int; UpstreamSummary() string }
func Sanitize(summary string) string               // 折叠空白、截 240 字、抹掉 sk-ant-*、Bearer *、≥40 位 [A-Za-z0-9+/=_-] 片段 → “[已隐藏]”
// fields.go
type Fields struct{ items []FieldError }
func (f *Fields) Required(field string); Invalid(field, msg string); Add(field, code, msg string)
func (f *Fields) Err() *Error                      // nil when empty; else VALIDATION_FAILED with fields and message “有 N 项资料不符合要求”
var FieldLabels = map[string]string{ "account":"账号", "password":"密码", "plan":"套餐", "register_location":"注册地区", "register_time":"注册日期", "plan_start_time":"套餐开始日期", "plan_end_time":"套餐结束日期", "platform_connection_id":"平台连接", "proxy_id":"交付代理", "auth_json.access_token":"访问令牌", "auth_json.refresh_token":"刷新令牌", "auth_json.id_token":"身份令牌", "auth_json.email":"凭证邮箱", "auth_json.account_uuid":"账号标识", "auth_json.organization_uuid":"组织标识", "auth_json.organization_name":"组织名称", "auth_json.last_refresh":"最近刷新时间", "auth_json.expired":"到期时间", "auth_json.proxy_url":"代理地址", "auth_json.type":"凭证类型", "auth_json.verification_status":"验证状态", "auth_json.claude_device_ids":"设备标识", "name":"名称", "scheme":"协议", "host":"主机地址", "port":"端口", "username":"用户名", "base_url":"平台地址", "token":"平台令牌", "timezone":"时区", "role_ids":"角色", "permissions":"权限", "page":"页码", "page_size":"每页数量" }
func Label(field string) string                    // FieldLabels[field] or field
```
`registry.go` 在 Task 1 只登记 `VALIDATION_FAILED`、`NOT_FOUND`、`INTERNAL_ERROR` 三条供测试，其余在 Task 2。

- [ ] **Step 1: 写失败测试**
```go
func TestRegistryInvariants(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range All() {
		if seen[d.Code] { t.Fatalf("duplicate %s", d.Code) }
		seen[d.Code] = true
		if !regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`).MatchString(d.Code) { t.Fatalf("bad name %s", d.Code) }
		if strings.TrimSpace(d.Message) == "" { t.Fatalf("%s has no message", d.Code) }
		for _, w := range []string{"JSON", "DB", "worker", "token", "payload", "抱歉", "对不起"} {
			if strings.Contains(d.Message, w) || strings.Contains(d.Hint, w) { t.Fatalf("%s uses banned word %q", d.Code, w) }
		}
		if d.Status != 0 && !statusAllowed(d.Category, d.Status) { t.Fatalf("%s: %s cannot use %d", d.Code, d.Category, d.Status) }
	}
}
func TestErrorBuilders(t *testing.T) {
	e := New("NOT_FOUND").WithDetail("resource", "account")
	if e.Status() != 404 || e.Details()["resource"] != "account" { t.Fatal(e) }
	mustPanic(t, func() { New("NO_SUCH_CODE") })
	mustPanic(t, func() { New("VALIDATION_FAILED").WithDetail("password", "x") })
	mustPanic(t, func() { New("INTERNAL_ERROR").WithMessage("custom") }) // not AllowCustomMessage
	var f Fields
	f.Required("password"); f.Invalid("plan_end_time", "套餐结束日期不能早于开始日期")
	v := f.Err()
	if v.Code() != "VALIDATION_FAILED" || len(v.Fields()) != 2 || v.Message() != "有 2 项资料不符合要求" { t.Fatal(v) }
	if v.Fields()[0].Message != "缺少密码" { t.Fatal(v.Fields()[0]) }
	if (&Fields{}).Err() != nil { t.Fatal("empty fields must be nil") }
	if got := Sanitize("rejected Bearer abc.def sk-ant-oat01-" + strings.Repeat("x", 50) + " done"); strings.Contains(got, "sk-ant") || strings.Contains(got, "abc.def") { t.Fatal(got) }
}
```
- [ ] **Step 2: 运行确认失败** — `go test ./internal/errs/`，期望包不存在 / 未定义。
- [ ] **Step 3: 实现 Interfaces**。`statusAllowed`：auth→{401,403}，permission→{403}，validation→{400,422}，not_found→{404}，conflict→{409}，rate_limit→{429}，upstream→{502,504}，config→{503}，storage→{503}，internal→{500}。`Required(field)` 的文案为 `缺少 + Label(field)`。
- [ ] **Step 4: 运行确认通过** — `go test ./internal/errs/` PASS；`go vet ./internal/errs/`。

### Task 2: 注册表全量登记（不改任何现有 code）

**Files:**
- Modify: `internal/errs/registry.go`
- Test: `internal/errs/registry_test.go`

**Interfaces (Produces):** 导出常量，每个 code 一个，例如 `const CodeValidationFailed = "VALIDATION_FAILED"`。登记范围（阶段 1 保持现名，阶段 2 才合并）：
- HTTP 54 个：`grep -rhoE '(fail\(w, *[^,]+, *|apiFail\([^,]+, *|serverError\(w, r, *)"[A-Z_]+"' internal/app/*.go | grep -v _test` 的全部结果，加 `billBad` 的 17 个、`HTTP_` 前缀除外。分类按 4.2；`Status` 取该 code 现在最常用的状态；`Message` 取现有最清楚的一句改写成陈述句；多文案的 code（`NOT_FOUND`、`PERMISSION_DENIED`、`VALIDATION_FAILED`、`PROXY_INVALID`、`PROXY_SETTINGS_INVALID`、`OAUTH_RESULT_INVALID`、`FILTER_INVALID`、`INVALID_JSON`、`ACCOUNT_DELIVERED`、`ACCOUNT_IN_PROGRESS`）设 `AllowCustomMessage: true`。
- 异步结果（`Status: 0`）：`PLATFORM_REJECTED`、`SUBMIT_OUTCOME_UNKNOWN`、`WORKER_RESTART_UNKNOWN`、`LOCAL_LINK_SAVE_FAILED`、`UNSUPPORTED_JOB`、`JOB_PAYLOAD_INVALID`、`ACCEPTED_PENDING_VERIFICATION`、`CREDENTIAL_UNREADABLE`、`PASSWORD_UNREADABLE`、`METADATA_UNREADABLE`、`PROXY_PROBE_FAILED`、`ALREADY_LINKED`、`SUBMIT_IN_PROGRESS`、`RETRY_EXHAUSTED`、`ACCOUNT_LINK_CHANGED`、`UPSTREAM_UNAVAILABLE`、9 个 `BILL_*`、`BILL_COST_INVALID`、`SYNC_SCOPE_TOO_LARGE`、`UPSTREAM_ID_INVALID`，以及代理探测 14 个：`PROXY_TIMEOUT`、`TARGET_AUTH_FAILED`、`TARGET_CONNECT_DENIED`、`TARGET_CONNECT_FAILED`、`TARGET_HTTP_ERROR`、`TARGET_REQUEST_FAILED`、`TARGET_RESPONSE_FAILED`、`UPSTREAM_AUTH_FAILED`、`UPSTREAM_CONNECT_DENIED`、`UPSTREAM_CONNECT_FAILED`、`UPSTREAM_TLS_FAILED`、`UPSTREAM_TUNNEL_FAILED`（`PROXY_INVALID`、`PROXY_SETTINGS_INVALID` 复用 HTTP 条目）。
- 预检 check 码（`Status: 0`，category validation）：`worker.go` `preflight()` 里 `add(...)` 的全部 20 个。
- 前端本地码：`NETWORK`、`TIMEOUT`、`RESPONSE_INVALID`（`Status: 0`，category internal，Doc 注明仅前端使用）。
- 文案来源：`web/src/account-workflow.ts` 的 `issues` 表是目前最完整的中文文案，优先采用并改成陈述句。

- [ ] **Step 1: 写失败测试**
```go
func TestRegistryCoversEveryCodeUsedInApp(t *testing.T) {
	// scan ../app for fail/apiFail/serverError/billBad/add(/finishJob string literals
	used := scanCodes(t, filepath.Join("..", "app"))
	for code := range used {
		if _, ok := Lookup(code); !ok { t.Errorf("code %s used in internal/app but not registered", code) }
	}
	if len(used) < 100 { t.Fatalf("scanner found only %d codes; regex broken?", len(used)) }
}
```
`scanCodes` 用 `go/parser` 遍历非测试文件，收集 `fail`/`apiFail`/`serverError`/`billBad`/`finishJob`/`finishJobWithMessage`/`add` 调用里第一个 `"[A-Z_]+"` 字面量，以及 `issues = append(issues, "X")` 形式。
- [ ] **Step 2: 运行确认失败** — 报出大量未登记 code。
- [ ] **Step 3: 登记全部 Def**（按上表），并在 `registry_test.go` 增加 `TestEveryDefHasDoc`（Doc 非空）。
- [ ] **Step 4: 运行确认通过** — `go test ./internal/errs/`。

### Task 3: `httpx` 门面与信封扩展，响应体带 request_id

**Files:**
- Modify: `internal/app/httpx.go:37-68, 284-321`、`internal/app/bill_sync.go:32-40`（`billBad`/`billFail`）
- Test: `internal/app/httpx_test.go`（新建）、`internal/app/security_test.go`（补 request_id 断言）

**Interfaces (Produces):**
```go
func writeError(w http.ResponseWriter, r *http.Request, err error)  // 唯一出口：errs.Error → 信封；其他 error → INTERNAL_ERROR 并 slog.Error(cause, request_id)
// 兼容门面（签名不变，内部构造 errs.Error；阶段 2 结束后由守卫测试禁止新调用）
func fail(w http.ResponseWriter, status int, code, message string)   // 用 errs.New(code)；message 非空且 def.AllowCustomMessage 时 WithMessage，否则忽略传入文案并保留注册表文案；status 与 def.Status 不一致时以 def 为准并 slog.Warn("status mismatch")
func serverError(w, r, code, message string, err error)
func apiFail(status int, code, message string) *apiError               // apiError 变成 errs.Error 的薄包装：type apiError = errs.Error 的别名不可行（方法集），改为 func apiFail(...) error 返回 *errs.Error，调用方类型 *apiError 全部改 error（onboard.go 15 处、account_delivery.go）
func respondError(w, r, err error)                                     // 转调 writeError
```
信封：`{"error":{"code","category","message","hint"(omitempty),"retryable","action","fields"(omitempty),"details"(omitempty)},"request_id"}`；`write()` 成功体加 `"request_id"`。request_id 来自 `w.Header().Get("X-Request-ID")`（中间件在 `httpx.go:199` 已先设置）。429 时若 `details.retry_after_seconds` 存在则同时写 `Retry-After` 头。

- [ ] **Step 1: 写失败测试**
```go
func TestErrorEnvelopeCarriesClassificationAndRequestID(t *testing.T) {
	_, admin := rbacApp(t) // permissions_test.go 的 helper
	res := admin.do(http.MethodGet, "/api/v1/accounts/missing", "")
	var body struct{ Error struct{ Code, Category, Message, Action string; Retryable bool; Details map[string]any }; RequestID string `json:"request_id"` }
	jsonDecode(res, &body)
	if res.StatusCode != 404 || body.Error.Code != "NOT_FOUND" || body.Error.Category != "not_found" || body.Error.Action != "none" || body.Error.Retryable || body.RequestID == "" || body.RequestID != res.Header.Get("X-Request-ID") { t.Fatalf("%+v", body) }
	ok := admin.do(http.MethodGet, "/api/v1/auth/session", "")
	var okBody map[string]any; jsonDecode(ok, &okBody)
	if okBody["request_id"] == "" { t.Fatal("success body lacks request_id") }
}
func TestLegacyFailKeepsRegistryMessageUnlessCustomAllowed(t *testing.T) {
	w := httptest.NewRecorder(); fail(w, 500, "INTERNAL_ERROR", "自定义")
	if !strings.Contains(w.Body.String(), errs.Lookup("INTERNAL_ERROR").Message) || strings.Contains(w.Body.String(), "自定义") { t.Fatal(w.Body.String()) }
	w = httptest.NewRecorder(); fail(w, 404, "NOT_FOUND", "代理不存在")
	if !strings.Contains(w.Body.String(), "代理不存在") { t.Fatal(w.Body.String()) }
}
```
加：`TestIdempotentReplayStillReturns202`（复用 `account_delivery_test` 的 `onboardRequest` 两次同 key，断言第二次 202 且 body 含 `request_id`）。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**；`apiError` 调用方改为 `error` / `*errs.Error`。
- [ ] **Step 4: 全量 Go 测试通过** — `go test -race -count=1 ./cmd/... ./internal/...`；`go vet ./...`。

### Task 4: 前端 `ApiError` 扩展与 `errors.ts` 解析层（行为不变）

**Files:**
- Modify: `web/src/api.ts:4-13, 48-60`
- Create: `web/src/errors.ts`
- Test: `web/tests/errors.test.ts`、`web/tests/api.test.ts`（补字段断言）

**Interfaces (Produces):**
```ts
// api.ts
export interface FieldError { field: string; code: string; message: string }
export class ApiError extends Error {
  constructor(public readonly status: number, public readonly code: string, message: string,
    public readonly extra: { category?: string; hint?: string; retryable?: boolean; action?: string; fields?: FieldError[]; details?: Record<string, unknown>; requestId?: string } = {})
  get category(): string  // extra.category ?? 本地推断（status 401/403→auth, 404→not_found, 409→conflict, 429→rate_limit, 5xx→internal, 0→internal, 4xx→validation）
  get fields(): FieldError[]; get requestId(): string | undefined; get action(): string; get retryable(): boolean; get hint(): string | undefined
}
// errors.ts
export interface ErrorView { title: string; detail?: string; requestId?: string; action: string; retryable: boolean; fields: FieldError[]; code: string; category: string }
export function describeError(err: unknown, fallback = '操作失败'): ErrorView   // 非 ApiError → {title: fallback 或 err.message, category:'internal', action:'none', fields:[]}
export function applyFieldErrors(fields: FieldError[], map: Record<string, string>): Record<string, string>  // JSON 路径 → 表单键；未映射的路径原样作键
export function errorClipboardText(view: ErrorView): string   // `${code}  ${title}\n${detail}\n请求编号 ${requestId}\n${new Date().toISOString()}`
```
`errorMessage()` 保留，内部改为 `describeError(err).title`。

- [ ] **Step 1: 写失败测试**
```ts
test('ApiError exposes the extended envelope and infers category when absent', () => {
  const e = new ApiError(422, 'VALIDATION_FAILED', '有 2 项资料不符合要求', { category: 'validation', fields: [{ field: 'auth_json.refresh_token', code: 'REQUIRED', message: '缺少刷新令牌' }], requestId: 'r1' })
  expect(e.fields).toHaveLength(1); expect(e.requestId).toBe('r1'); expect(e.category).toBe('validation')
  expect(new ApiError(409, 'VERSION_CONFLICT', 'x').category).toBe('conflict')
  expect(new ApiError(0, 'NETWORK', 'x').category).toBe('internal')
})
test('describeError and applyFieldErrors map an envelope onto a form', () => {
  const view = describeError(new ApiError(422, 'VALIDATION_FAILED', '有 2 项资料不符合要求', { hint: '请修改标红的字段', fields: [{ field: 'auth_json.refresh_token', code: 'REQUIRED', message: '缺少刷新令牌' }, { field: 'plan_end_time', code: 'OUT_OF_RANGE', message: '套餐结束日期不能早于开始日期' }], requestId: 'r2' }))
  expect(view.title).toBe('有 2 项资料不符合要求'); expect(view.detail).toBe('请修改标红的字段'); expect(view.requestId).toBe('r2')
  expect(applyFieldErrors(view.fields, { 'auth_json.refresh_token': 'refresh_token' })).toEqual({ refresh_token: '缺少刷新令牌', plan_end_time: '套餐结束日期不能早于开始日期' })
  expect(describeError(new Error('boom')).title).toBe('boom'); expect(describeError('x', '操作失败').title).toBe('操作失败')
  expect(errorClipboardText(view)).toContain('请求编号 r2')
})
```
在 `api.test.ts` 补：模拟响应 `{error:{code,category,fields,...},request_id}` → 抛出的 `ApiError.fields` 与 `requestId` 正确；缺 `request_id` 时回退读响应头 `X-Request-ID`。
- [ ] **Step 2: 运行确认失败** — `npm test -- --reporter=dot tests/errors.test.ts tests/api.test.ts`。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: `npm test`、`npm run lint`、`npm run build` 通过**。

## 阶段 2：收敛调用点

### Task 5: 账号 / 上号 / 凭证校验改为全部收集

**Files:**
- Modify: `internal/app/validation.go`（`validateAuth`→`validateAuthFields`、`validAccountDates`、`validAccountIdentity`、`validOptionalMetadata` 改返回 `[]errs.FieldError`）、`internal/app/onboard.go:60-160`、`internal/app/app.go`（`accounts` POST、`accountPatch`、`accountCredentials`）、`internal/app/worker.go:425-520`（`preflight` 用同一套校验）
- Test: `internal/app/validation_test.go`、`internal/app/account_delivery_test.go`、`internal/app/app_test.go`

**Interfaces (Produces):**
```go
func validateAuthFields(auth map[string]any) []errs.FieldError            // field 形如 "auth_json.refresh_token"，code REQUIRED / INVALID_FORMAT / UNSUPPORTED_VALUE
func validateAccountFields(in map[string]any, requireDates bool) []errs.FieldError // account, plan, register_location, dates, metadata
func (a *App) validateOnboard(req *onboardInput) error                     // 聚合上面两个 + password + platform_connection_id + proxy，返回 (&errs.Fields{}).Err()
```
预检 `checks[]` 元素从 `{code,status}` 变为 `{code,status,message,field?}`，`code` 继续用现有 20 个预检码，`message` 取注册表，`field` 为 JSON 路径（有则给）。

- [ ] **Step 1: 写失败测试**
```go
func TestOnboardReportsEveryProblemAtOnce(t *testing.T) {
	a, _, _, _ := deliveryFixture(t)
	body := deliveryBody(); delete(body, "password"); body["plan_end_time"] = "2026-10-01"
	body["auth_json"].(map[string]any)["refresh_token"] = ""
	w := onboardRequest(a, body, "all-at-once")
	var env struct{ Error struct{ Code string; Fields []struct{ Field, Code string } } }
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != 422 || env.Error.Code != "VALIDATION_FAILED" || len(env.Error.Fields) != 3 { t.Fatal(w.Body.String()) }
	want := map[string]string{"password": "REQUIRED", "plan_end_time": "OUT_OF_RANGE", "auth_json.refresh_token": "REQUIRED"}
	for _, f := range env.Error.Fields { if want[f.Field] != f.Code { t.Fatalf("%+v", f) } }
}
func TestPreflightChecksCarryMessages(t *testing.T) { /* 创建草稿账号 → preflight → 每个 status=error 的 check 都有非空 message，PASSWORD_REQUIRED 的 field=="password" */ }
```
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**；删除 `validateAuth` 的 `[]string` 版本及其 `AUTH_*_REQUIRED` 字符串拼接。
- [ ] **Step 4: 全量 Go 测试通过**（修正依赖旧单条错误文案的既有断言，逐条列在 ledger）。

### Task 6: 其余资源收敛与 code 合并

**Files:**
- Modify: `internal/app/advanced.go`（代理、导入）、`internal/app/users.go`、`internal/app/rbac.go`、`internal/app/app.go`（平台连接、分页、筛选）、`internal/app/security.go`、`internal/app/proxy_network.go`、`internal/app/pagination.go`、`internal/app/lookups.go`、`internal/app/usage.go`、`internal/app/audit.go`、`internal/app/bill_routes.go`、`internal/app/bill_sync.go`、`internal/app/claude_oauth.go`、`internal/app/ratelimit.go`
- Modify: `internal/errs/registry.go`（合并后删除旧 code）
- Test: 对应 `*_test.go` 的期望 code

**合并表（旧 → 新）：**
| 旧 | 新 | 备注 |
| --- | --- | --- |
| `DUPLICATE_ACCOUNT` | `ACCOUNT_EXISTS` | 409 |
| `INVALID_JSON`、`MALFORMED_JSON`、`VALIDATION_FAILED("内容不是合法 JSON")` | `REQUEST_MALFORMED` | 400 |
| `PAGE_INVALID`、`FILTER_INVALID` | `QUERY_INVALID` + `fields[]`（`page`/`page_size`/筛选键） | 400 |
| `DB_ERROR` | `STORAGE_UNAVAILABLE`（`sqlite` busy/locked/readonly/disk full）或 `INTERNAL_ERROR` | 503 / 500，判定函数 `isStorageUnavailable(err)` |
| `ENCRYPTION_FAILED`、`CONNECTION_UNREADABLE`、`CREDENTIAL_UNREADABLE`、`PASSWORD_UNREADABLE`、`METADATA_UNREADABLE` | `SECRET_UNREADABLE` + `details.secret` ∈ {account_password, account_credentials, connection_token, proxy_password, metadata} | 503 config |
| `AUTH_REQUIRED("账号已停用或不存在")` | `USER_DISABLED` | 401 |
| `PERMISSION_DENIED`（5 种文案） | `PERMISSION_DENIED` + `details.permission`，文案统一“当前账号没有执行此操作的权限” | 403 |
| `TOKEN_REQUIRED`、`PASSWORD_REQUIRED`（HTTP 层）、`ACCOUNT_REQUIRED`、`PLATFORM_CONNECTION_REQUIRED`（HTTP 层） | `VALIDATION_FAILED.fields[]` | 预检 / 任务里的同名码保留 |
| `billBad` 的 17 个请求校验码 | `VALIDATION_FAILED.fields[]`（`scope`、`timezone`、`accounts`、`connection_id`、`idempotency_key`）；`NO_LINKED_ACCOUNTS`、`ACCOUNT_LINK_MISSING`、`SYNC_SCOPE_TOO_LARGE` 保留为独立 422 码 | |
| `OAUTH_RESULT_INVALID` 的 12 种文案 | 保留一个 code，`AllowCustomMessage` | |

- [ ] **Step 1: 写失败测试**（每行一条，示例）
```go
func TestMergedCodes(t *testing.T) {
	a, admin := rbacApp(t)
	res := admin.do(http.MethodPost, "/api/v1/accounts", `{"account":`) // 语法错误
	assertCode(t, res, 400, "REQUEST_MALFORMED")
	res = admin.do(http.MethodGet, "/api/v1/accounts?page=0&page_size=7", "")
	assertCodeFields(t, res, 400, "QUERY_INVALID", "page", "page_size")
	// 两次创建同账号 → ACCOUNT_EXISTS；viewer 创建代理 → PERMISSION_DENIED 且 details.permission=="proxies.create"
	// 写限速触发 → 429，Retry-After 头 == details.retry_after_seconds
}
```
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 按合并表替换调用点**；删除注册表中的旧 code；`isStorageUnavailable` 用 modernc sqlite 错误码 `SQLITE_BUSY`/`SQLITE_LOCKED`/`SQLITE_FULL`/`SQLITE_READONLY`/`SQLITE_IOERR`。
- [ ] **Step 4: 全量 Go 测试通过**。

### Task 7: 导入预览 `issues` 结构化

**Files:**
- Modify: `internal/app/advanced.go:55-100`
- Modify: `web/src/types/api.ts:261`、`web/src/components/ImportDialog.vue`
- Test: `internal/app/advanced_test.go`、`web/tests/import-dialog.test.ts`（新建）

**Interfaces (Produces):** `rows[].issues: []{code, message, field?}`（原 `[]string`）。这是唯一一处成功响应的形状变化，消费者只有 `ImportDialog.vue`。

- [ ] **Step 1: 写失败测试** — Go：预览含缺密码行 → `issues[0] == {code:"PASSWORD_REQUIRED", message:"缺少密码", field:"password"}`；前端：渲染 `issues` 的 `message` 并以 `code` 为 `title`。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: Go 与 vitest 通过**。

### Task 8: 异步结果、上游摘要与 `allowed_actions`

**Files:**
- Modify: `internal/app/worker.go:93-230, 250-256, 410-420`、`internal/app/account_delivery.go:100-135`、`internal/app/bill_sync.go`（`failQueuedBillItemTx`、同步项读取）、`internal/app/proxy_network.go`（检测结果读取）
- Modify: `internal/supplier/client.go`、`internal/supplier/bills.go`、`internal/claudeoauth/client.go`（实现 `errs.Upstream`）
- Test: `internal/app/account_delivery_test.go`、`internal/app/bills_test.go`、`internal/app/proxy_network_test.go`、`internal/supplier/client_test.go`、`internal/claudeoauth/client_test.go`

**Interfaces (Produces):**
```go
// worker.go
func (a *App) finishJob(id, state string, err *errs.Error)   // result_json = {"code","message","hint","retryable","action","details"}
func outcomeOf(err error) *errs.Error                         // errs.Upstream → PLATFORM_* / OAUTH_*，details.upstream_status，message 追加 “平台返回：”+Sanitize(Summary)
func allowedActions(state string, e *errs.Error) []string     // failed+ActionRetry→["retry"], unknown→["check_platform","associate_platform_id"], pending→["submit"], linked→["open_platform"]
// 读取派生：账号列表 submission_job 增加 message(如缺)、retryable、action、allowed_actions；bill_sync_items / proxy_checks 读取时按 error_code 查注册表补 message/action
// supplier
type SubmitError struct{ Status int; Kind string /* rejected|unknown|auth|invalid_response */; Summary string }  // 实现 errs.Upstream：UpstreamCode: rejected→PLATFORM_REJECTED, auth→PLATFORM_AUTH_FAILED, invalid_response→PLATFORM_RESPONSE_INVALID, unknown→SUBMIT_OUTCOME_UNKNOWN
// BillHTTPError 实现 Upstream：UpstreamCode: 401/403→PLATFORM_AUTH_FAILED，5xx/timeout→PLATFORM_UNAVAILABLE，其他→PLATFORM_RESPONSE_INVALID
// claudeoauth：request() 的三类错误改为 *OAuthError{Kind connect|status|decode; Status} 实现 Upstream → OAUTH_EXCHANGE_FAILED，Summary 为空（授权响应绝不摘要）
```
删除 `sanitizeUpstream`，改用 `errs.Sanitize`。新增注册表码：`PLATFORM_AUTH_FAILED`、`PLATFORM_RESPONSE_INVALID`、`PLATFORM_UNAVAILABLE`。

- [ ] **Step 1: 写失败测试**
```go
func TestRejectedSubmissionStoresStructuredOutcome(t *testing.T) {
	// deliveryFixture mode=1 → runOne → jobs.result_json 含 code PLATFORM_REJECTED、retryable true、action "fix_input"、details.upstream_status 422、message 含 “平台返回：” 且不含 "fixture-password"
	// 账号列表 submission_job.allowed_actions == ["retry"]
}
func TestLegacyResultJsonGainsMessageOnRead(t *testing.T) { /* 手工 UPDATE jobs SET result_json='{"code":"PLATFORM_REJECTED"}' → 列表 submission_job.message == 注册表文案 */ }
func TestUpstreamSummaryNeverLeaksSecrets(t *testing.T) { /* SubmitError{Summary:"Bearer sk-ant-..."} → outcomeOf().Message() 不含 sk-ant */ }
```
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 全量 Go 测试通过**。

### Task 9: 代码生成与守卫

**Files:**
- Create: `internal/errs/gen/main.go`（`//go:generate go run ./gen` 于 `internal/errs/doc.go`）
- Create (generated): `web/src/generated/error-codes.ts`、`docs/errors.md`
- Create: `internal/app/errs_guard_test.go`
- Modify: `Makefile`（`generate` 与 `check-generated` 目标；`lint` 依赖 `check-generated`）、`.github/workflows/*.yml`（调用 `make check-generated`）

**Interfaces (Produces):**
```ts
// web/src/generated/error-codes.ts
export const ErrorCodes = { VALIDATION_FAILED: 'VALIDATION_FAILED', ... } as const
export type ErrorCode = keyof typeof ErrorCodes
export const ErrorCategories = ['auth', ...] as const
export const ErrorActions = ['none', ...] as const
export const FieldLabels: Record<string, string> = { ... }
export const ErrorMessages: Record<ErrorCode, string> = { ... }   // 兜底文案（历史数据无 message 时）
```
`docs/errors.md`：表格 code | 分类 | HTTP | 可重试 | 动作 | 文案 | 说明。

- [ ] **Step 1: 写失败测试** — `errs_guard_test.go`：用 `go/ast` 扫 `internal/app` 非测试文件，`fail`/`apiFail`/`serverError` 的调用总数必须为 0（阶段 2 后全部改为 `writeError(w, r, errs.New(...))` / 返回 `error`），并且任何 `errs.New("...")` 的字面量都能 `Lookup`。`gen` 的测试：生成到临时目录后与仓库内文件 `bytes.Equal`。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现生成器与 Makefile 目标；把残余 `fail`/`apiFail`/`serverError` 调用改为 `writeError`**，然后删除这三个函数。
- [ ] **Step 4: `make generate && make check-generated && go test -race ./...` 通过**。

## 阶段 3：前端接入

### Task 10: `ErrorNotice.vue`、字段级错误与映射表下线

**Files:**
- Create: `web/src/components/ErrorNotice.vue`
- Modify: 15 个含 `errorMessage(` 的组件 / 视图（见 `grep -ln 'errorMessage(' web/src/components/*.vue web/src/views/*.vue`）；六个表单加 `applyFieldErrors`：`AccountFormDialog.vue`（映射 `auth_json.*`→凭证表单键、`password`、`plan*`、`register_*`、`platform_connection_id`、`proxy_id`）、`ProxyFormDialog.vue`、`ConnectionFormDialog.vue`、`users/UserFormDialog.vue`、`users/RoleEditorDialog.vue`、`ImportDialog.vue`
- Modify: `web/src/account-workflow.ts`（删除 `issues` 表与 `deliveryIssue`；新增 `outcomeText(job)`：`job.message ?? ErrorMessages[job.code] ?? '请核对账号资料与平台配置'`）
- Modify: `web/src/api.ts`（401/403 跳转按 code `AUTH_REQUIRED`/`USER_DISABLED`/`CSRF_FAILED`；`RATE_LIMITED` 全局 toast 带秒数）
- Test: `web/tests/error-notice.test.ts`、`web/tests/account-form-dialog.test.ts`（字段级）、`web/tests/proxy-form-dialog.test.ts`、`web/tests/account-workflow.test.mjs`

**Interfaces (Produces):**
```ts
// ErrorNotice.vue props: { error: unknown | null; fallback?: string; compact?: boolean }  emits: close
// 渲染 describeError：title、detail(hint)、fields 列表（compact 时隐藏，交给表单内联）、request_id + “复制错误信息”按钮（navigator.clipboard.writeText(errorClipboardText(view))）
// 颜色：validation/conflict→琥珀 (--pending)，auth/permission/config/storage/internal→红 (--danger)，upstream/rate_limit→灰 (--muted)
```
表单内联：`el-form-item` 的 `:error="fieldErrors[key]"`，提交成功或字段被编辑时清除该键。

- [ ] **Step 1: 写失败测试** — `ErrorNotice` 渲染 422 信封：标题、提示、request_id、复制按钮调用 clipboard；`AccountFormDialog` 收到 422 `fields[auth_json.refresh_token, plan_end_time]` 后 `#field-plan-end` 显示字段 message 且顶部不再列出字段明细；`account-workflow`：`outcomeText({code:'PLATFORM_REJECTED'})` 等于生成常量里的文案。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**；删除 `deliveryIssue` 及其 37 条映射。
- [ ] **Step 4: `npm test`、`npm run lint`、`npm run format:check`、`npm run build` 通过**。

### Task 11: Playwright 用例

**Files:**
- Create: `web/e2e/errors.spec.ts`
- Modify: `web/e2e/claude-oauth.spec.ts`（断言文案若有变化）

- [ ] **Step 1: 写用例** — (a) 新建账号对话框手填不合法起止日期与空密码后提交 → 两个字段内联报错同时出现，顶部通知含“有 N 项资料不符合要求”；(b) 用 `page.route` 把 `/api/v1/proxies` POST 改成 503 `STORAGE_UNAVAILABLE` 信封 → 通知显示请求编号且“复制错误信息”后剪贴板含该编号（`context.grantPermissions(['clipboard-read'])`）；(c) 只读用户删除代理 → 403 通知文案“当前账号没有执行此操作的权限”。
- [ ] **Step 2: `npx playwright test` 全部 PASS**。

## 阶段 4：文档与护栏

### Task 12: 契约文档与 CI

**Files:**
- Modify: `docs/api-contract.md` 1.2 节（改为描述新信封并链接 `docs/errors.md`；删除未实现码表，保留 HTTP→分类表）、`docs/claude-oauth-onboarding.md`、`docs/platform-contract.md`（引用 `PLATFORM_*`）
- Modify: `Makefile` `test` 目标加 `check-generated`；`.github/workflows` 加该步骤
- Modify: `web/src/components.d.ts` 若生成器触发变化

- [ ] **Step 1: 改文档**，并在 `docs/errors.md` 顶部加“由 `make generate` 生成，勿手改”。
- [ ] **Step 2: 本地跑 `make test`（go-vet、go-test、web-test、check-generated）与 `make build`，全部通过**。
- [ ] **Step 3: 复核 Review Focus 五项各有测试**，在 ledger 记录对应测试名。
