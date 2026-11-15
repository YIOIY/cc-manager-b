package errs

import "fmt"

// Fields collects every validation problem of one request so the user sees
// them all at once instead of one per submit.
type Fields struct{ items []FieldError }

// Required records a missing input.
func (f *Fields) Required(field string) {
	f.Add(field, FieldRequired, "缺少"+Label(field))
}

// Invalid records a malformed input with a specific message.
func (f *Fields) Invalid(field, msg string) { f.Add(field, FieldInvalidFormat, msg) }

// Add records a problem with an explicit field code.
func (f *Fields) Add(field, code, msg string) {
	f.items = append(f.items, FieldError{Field: field, Code: code, Message: msg})
}

// Len reports how many problems were collected.
func (f *Fields) Len() int { return len(f.items) }

// Items returns the collected problems.
func (f *Fields) Items() []FieldError { return f.items }

// Err returns nil when nothing was collected, otherwise VALIDATION_FAILED
// carrying every problem.
func (f *Fields) Err() *Error {
	if len(f.items) == 0 {
		return nil
	}
	return New("VALIDATION_FAILED").
		WithMessage(fmt.Sprintf("有 %d 项资料不符合要求", len(f.items))).
		WithFields(f.items...)
}

// FieldLabels maps JSON paths to the labels users see; the frontend receives
// the same table through code generation so the two never drift.
var FieldLabels = map[string]string{
	"account": "账号", "password": "密码", "plan": "套餐", "register_location": "注册地区",
	"register_time": "注册日期", "plan_start_time": "套餐开始日期", "plan_end_time": "套餐结束日期",
	"platform_connection_id": "平台连接", "proxy_id": "交付代理",
	"auth_json":              "Claude 凭证",
	"auth_json.access_token": "访问令牌", "auth_json.refresh_token": "刷新令牌", "auth_json.id_token": "身份令牌",
	"auth_json.email": "凭证邮箱", "auth_json.account_uuid": "账号标识", "auth_json.organization_uuid": "组织标识",
	"auth_json.organization_name": "组织名称", "auth_json.last_refresh": "最近刷新时间", "auth_json.expired": "到期时间",
	"auth_json.proxy_url": "代理地址", "auth_json.type": "凭证类型", "auth_json.verification_status": "验证状态",
	"auth_json.claude_device_ids": "设备标识",
	"name":                        "名称", "scheme": "协议", "host": "主机地址", "port": "端口", "username": "用户名", "nickname": "昵称",
	"base_url": "平台地址", "token": "平台令牌", "timezone": "时区", "role_ids": "角色", "permissions": "权限",
	"page": "页码", "page_size": "每页数量",
	"remark": "备注", "payment_method": "支付方式", "register_device": "注册设备", "recharge_time": "充值时间",
	"balance": "余额百分比", "warranty_period": "质保期", "tags": "标签",
	"connection_id": "平台连接", "accounts": "账号列表", "scope": "同步范围", "idempotency_key": "重复提交保护键",
	"upstream_url": "前置代理地址", "mode": "检测模式", "timeout_seconds": "检测超时", "enabled": "启用状态", "description": "描述",
	"sort": "排序", "state": "状态", "revision_min": "修订次数", "from": "开始日期", "to": "结束日期", "content": "导入内容",
	"onboarding_state": "上号状态", "archived": "库存状态", "proxy": "代理绑定", "clear_password": "清除密码",
}

// Label returns the user-facing label for a field path, or the path itself.
func Label(field string) string {
	if l, ok := FieldLabels[field]; ok {
		return l
	}
	return field
}
