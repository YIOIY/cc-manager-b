package app

import (
	"cc-manager/internal/errs"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var allowedPaymentMethods = map[string]bool{
	"physical_credit_card": true, "virtual_credit_card": true, "paypal": true,
	"apple_pay": true, "google_pay": true, "third_party_payment": true,
}

func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

// validOptionalMetadataFields reports every problem in the supplier-defined
// optional fields, one FieldError per key.
func validOptionalMetadataFields(in map[string]any) []errs.FieldError {
	var f errs.Fields
	if v, ok := in["remark"]; ok && v != nil && v != "" {
		if _, ok := v.(string); !ok {
			f.Invalid("remark", "备注必须是文本")
		}
	}
	if v, ok := in["payment_method"]; ok && v != nil && v != "" && !allowedPaymentMethods[fmt.Sprint(v)] {
		f.Add("payment_method", errs.FieldUnsupportedValue, "支付方式不在允许范围内")
	}
	if v, ok := in["register_device"]; ok && v != nil && v != "" && v != "Mac" && v != "Windows" && v != "Linux" {
		f.Add("register_device", errs.FieldUnsupportedValue, "注册设备只能是 Mac、Windows 或 Linux")
	}
	if v, ok := in["recharge_time"]; ok && v != nil && v != "" && !validDate(fmt.Sprint(v)) {
		f.Invalid("recharge_time", "充值时间必须是 YYYY-MM-DD 格式的日期")
	}
	if v, ok := in["balance"]; ok && v != nil && v != "" {
		n, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		if err != nil || n < 0 || n > 100 || n != n {
			f.Add("balance", errs.FieldOutOfRange, "余额百分比必须在 0 到 100 之间")
		}
	}
	if v, ok := in["warranty_period"]; ok && v != nil && v != "" {
		n, err := strconv.Atoi(fmt.Sprint(v))
		if err != nil || (n != 1 && n != 7 && n != 30) {
			f.Add("warranty_period", errs.FieldUnsupportedValue, "质保期只能是 1、7 或 30 天")
		}
	}
	if v, ok := in["tags"]; ok && v != nil {
		tags, ok := v.([]any)
		if !ok {
			f.Invalid("tags", "标签必须是文本列表")
		} else {
			seen := map[string]bool{}
			for _, item := range tags {
				tag, ok := item.(string)
				if !ok || (tag != "速刷" && tag != "慢蒸") || seen[tag] {
					f.Add("tags", errs.FieldUnsupportedValue, "标签只能是“速刷”或“慢蒸”，且不能重复")
					break
				}
				seen[tag] = true
			}
			if seen["速刷"] && seen["慢蒸"] {
				f.Add("tags", errs.FieldMismatch, "速刷与慢蒸不能同时使用")
			}
		}
	}
	return f.Items()
}

// validOptionalMetadata keeps the single-error form for callers that only
// need a yes/no answer.
func validOptionalMetadata(in map[string]any) error {
	if problems := validOptionalMetadataFields(in); len(problems) > 0 {
		return fmt.Errorf("%s", problems[0].Message)
	}
	return nil
}

// validateAuthFields reports every problem in a Claude credential object.
// Field paths are prefixed with auth_json so they match the request body.
func validateAuthFields(auth map[string]any) []errs.FieldError {
	var f errs.Fields
	for _, field := range requiredAuth {
		value, ok := auth[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			f.Required("auth_json." + field)
		}
	}
	if _, ok := auth["id_token"].(string); !ok {
		f.Add("auth_json.id_token", errs.FieldRequired, "缺少身份令牌字段（允许为空）")
	}
	if ids, ok := auth["claude_device_ids"].([]any); !ok || len(ids) == 0 {
		f.Required("auth_json.claude_device_ids")
	} else {
		for _, id := range ids {
			value, ok := id.(string)
			if !ok || strings.TrimSpace(value) == "" {
				f.Invalid("auth_json.claude_device_ids", "设备标识必须是非空文本")
				break
			}
		}
	}
	for _, key := range []string{"last_refresh", "expired"} {
		if value, ok := auth[key].(string); ok && value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				f.Invalid("auth_json."+key, errs.Label("auth_json."+key)+"必须是 RFC3339 格式的时间")
			}
		}
	}
	if value, ok := auth["email"].(string); ok && value != "" {
		if _, err := mail.ParseAddress(value); err != nil {
			f.Invalid("auth_json.email", "凭证邮箱格式不正确")
		}
	}
	if value, ok := auth["proxy_url"].(string); ok && value != "" {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			f.Add("auth_json.proxy_url", errs.FieldUnsupportedValue, "代理地址必须以 http://、https://、socks5:// 或 socks5h:// 开头")
		}
	}
	return f.Items()
}

// validateAuth keeps the legacy code list for callers not yet converted.
func validateAuth(auth map[string]any) []string {
	out := []string{}
	for _, p := range validateAuthFields(auth) {
		suffix := "_INVALID"
		if p.Code == errs.FieldRequired {
			suffix = "_REQUIRED"
		}
		out = append(out, "AUTH_"+strings.ToUpper(strings.TrimPrefix(p.Field, "auth_json."))+suffix)
	}
	return out
}

// Shared vocabularies. Every handler that accepts an account must agree on
// these; keeping them in one place removes the four copies that drifted apart.
var allowedPlans = map[string]bool{"claude/cc-max-20x": true, "claude/cc-max-5x": true, "claude/cc-pro": true}

var allowedProxySchemes = map[string]bool{"http": true, "https": true, "socks5": true, "socks5h": true}

// optionalMetadataKeys are the supplier-defined optional fields stored in
// accounts.metadata_json and forwarded verbatim on submission.
var optionalMetadataKeys = []string{"balance", "tags", "remark", "payment_method", "register_device", "recharge_time", "warranty_period"}

const supportedRegisterLocation = "US"

func validPlan(plan string) bool { return allowedPlans[plan] }

func validProxyScheme(scheme string) bool { return allowedProxySchemes[strings.ToLower(scheme)] }

// accountIdentityField reports the account problem as a FieldError, or nil.
func accountIdentityField(account string) *errs.FieldError {
	if account == "" {
		return &errs.FieldError{Field: "account", Code: errs.FieldRequired, Message: "缺少账号"}
	}
	if strings.ContainsAny(account, " \t\r\n") {
		return &errs.FieldError{Field: "account", Code: errs.FieldInvalidFormat, Message: "账号不能包含空格"}
	}
	if strings.Contains(account, "@") {
		if _, err := mail.ParseAddress(account); err != nil {
			return &errs.FieldError{Field: "account", Code: errs.FieldInvalidFormat, Message: "账号邮箱格式不正确"}
		}
	}
	return nil
}

// validAccountIdentity accepts a login id or an e-mail address. Whitespace is
// never allowed; anything containing @ must parse as an address.
func validAccountIdentity(account string) error {
	if account == "" {
		return fmt.Errorf("账号不能为空")
	}
	if strings.ContainsAny(account, " \t\r\n") {
		return fmt.Errorf("账号不能包含空格")
	}
	if strings.Contains(account, "@") {
		if _, err := mail.ParseAddress(account); err != nil {
			return fmt.Errorf("账号邮箱格式不正确")
		}
	}
	return nil
}

// pickMetadata copies the optional supplier fields out of a request body,
// dropping empty values so they are not sent as "" upstream.
func pickMetadata(in map[string]any) map[string]any {
	metadata := map[string]any{}
	for _, key := range optionalMetadataKeys {
		if value, ok := in[key]; ok && value != nil && value != "" {
			metadata[key] = value
		}
	}
	return metadata
}

// accountDateFields reports format and ordering problems of the three account
// dates, one per field. Empty values are allowed when required is false.
func accountDateFields(registered, start, end string, required bool) []errs.FieldError {
	var f errs.Fields
	valid := map[string]bool{}
	for _, item := range []struct{ key, value string }{{"register_time", registered}, {"plan_start_time", start}, {"plan_end_time", end}} {
		if item.value == "" {
			if required {
				f.Required(item.key)
			}
			continue
		}
		if !validDate(item.value) {
			f.Invalid(item.key, errs.Label(item.key)+"必须是 YYYY-MM-DD 格式的日期")
			continue
		}
		valid[item.key] = true
	}
	if valid["register_time"] && valid["plan_start_time"] && registered > start {
		f.Add("register_time", errs.FieldOutOfRange, "注册日期不能晚于套餐开始日期")
	}
	if valid["plan_start_time"] && valid["plan_end_time"] && start > end {
		f.Add("plan_end_time", errs.FieldOutOfRange, "套餐结束日期不能早于开始日期")
	}
	return f.Items()
}

// validateAccountFields checks the profile part of an account body: identity,
// plan, location, dates and optional metadata. It never stops at the first
// problem so the user can fix everything in one round.
func validateAccountFields(in map[string]any, requireDates bool) []errs.FieldError {
	var f errs.Fields
	if p := accountIdentityField(str(in, "account")); p != nil {
		f.Add(p.Field, p.Code, p.Message)
	}
	if plan := str(in, "plan"); plan == "" {
		f.Required("plan")
	} else if !validPlan(plan) {
		f.Add("plan", errs.FieldUnsupportedValue, "套餐不在允许范围内")
	}
	if loc := str(in, "register_location"); (requireDates || loc != "") && loc != supportedRegisterLocation {
		f.Add("register_location", errs.FieldUnsupportedValue, "注册地区当前仅支持美国（US）")
	}
	for _, p := range accountDateFields(str(in, "register_time"), str(in, "plan_start_time"), str(in, "plan_end_time"), requireDates) {
		f.Add(p.Field, p.Code, p.Message)
	}
	for _, p := range validOptionalMetadataFields(in) {
		f.Add(p.Field, p.Code, p.Message)
	}
	return f.Items()
}

// validAccountDates checks format and ordering of the three account dates.
// Empty values are allowed when required is false (draft accounts).
func validAccountDates(registered, start, end string, required bool) error {
	for _, item := range []struct{ key, value string }{{"register_time", registered}, {"plan_start_time", start}, {"plan_end_time", end}} {
		if item.value == "" {
			if required {
				return fmt.Errorf("%s 必须为 YYYY-MM-DD", item.key)
			}
			continue
		}
		if !validDate(item.value) {
			return fmt.Errorf("%s 必须为 YYYY-MM-DD", item.key)
		}
	}
	if (registered != "" && start != "" && registered > start) || (start != "" && end != "" && start > end) {
		return fmt.Errorf("账号日期顺序不正确")
	}
	return nil
}

// proxyFieldErrors checks a proxy's scheme, host and port together.
func proxyFieldErrors(scheme, host string, port int) *errs.Error {
	var f errs.Fields
	if scheme == "" {
		f.Required("scheme")
	} else if !validProxyScheme(scheme) {
		f.Add("scheme", errs.FieldUnsupportedValue, "代理协议只能是 http、https、socks5 或 socks5h")
	}
	if host == "" {
		f.Required("host")
	}
	if port < 1 || port > 65535 {
		f.Add("port", errs.FieldOutOfRange, "端口必须在 1 到 65535 之间")
	}
	if f.Len() == 0 {
		if _, err := proxyEndpoint((&url.URL{Scheme: scheme, Host: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))}).String()); err != nil {
			f.Invalid("host", err.Error())
		}
	}
	return f.Err()
}
