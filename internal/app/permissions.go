package app

// Permissions are "<module>.<action>". The catalogue below is the single
// source of truth: the router checks these names, the role editor renders
// them grouped by module, and builtin roles are defined in terms of them.
type Permission string

type permissionDef struct {
	Key   Permission `json:"key"`
	Label string     `json:"label"`
	Hint  string     `json:"hint,omitempty"`
}

type permissionModule struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Page names the navigation entry the module's ".view" permission reveals;
	// empty for modules that live inside another page.
	Page        string          `json:"page,omitempty"`
	Permissions []permissionDef `json:"permissions"`
}

const (
	PermDashboardView        Permission = "dashboard.view"
	PermAccountsView         Permission = "accounts.view"
	PermAccountsCreate       Permission = "accounts.create"
	PermAccountsUpdate       Permission = "accounts.update"
	PermAccountsArchive      Permission = "accounts.archive"
	PermAccountsDelete       Permission = "accounts.delete"
	PermAccountsImport       Permission = "accounts.import"
	PermAccountsCredential   Permission = "accounts.credentials"
	PermAccountsOnboard      Permission = "accounts.onboard"
	PermAccountsUsage        Permission = "accounts.usage"
	PermAccountsPlatformEdit Permission = "accounts.platform_edit"
	PermProxiesView          Permission = "proxies.view"
	PermProxiesCreate        Permission = "proxies.create"
	PermProxiesArchive       Permission = "proxies.archive"
	PermProxiesUpdate        Permission = "proxies.update"
	PermProxiesDelete        Permission = "proxies.delete"
	PermProxiesTest          Permission = "proxies.test"
	PermProxiesSettings      Permission = "proxies.settings"
	PermJobsView             Permission = "jobs.view"
	PermBillsView            Permission = "bills.view"
	PermBillsQuery           Permission = "bills.query"
	PermBillsSync            Permission = "bills.sync"
	PermConnectionsView      Permission = "connections.view"
	PermConnectionsCreate    Permission = "connections.create"
	PermConnectionsUpdate    Permission = "connections.update"
	PermConnectionsDelete    Permission = "connections.delete"
	PermAuditView            Permission = "audit.view"
	PermUsersView            Permission = "users.view"
	PermUsersCreate          Permission = "users.create"
	PermUsersUpdate          Permission = "users.update"
	PermUsersDelete          Permission = "users.delete"
	PermUsersResetPassword   Permission = "users.reset_password"
	PermRolesView            Permission = "roles.view"
	PermRolesManage          Permission = "roles.manage"
)

var permissionCatalogue = []permissionModule{
	{"dashboard", "仪表盘", "仪表盘", []permissionDef{{PermDashboardView, "查看仪表盘", ""}}},
	{"accounts", "账号管理", "账号管理", []permissionDef{
		{PermAccountsView, "查看账号列表与详情", "含上号预检结果"},
		{PermAccountsCreate, "新建账号草稿", ""},
		{PermAccountsUpdate, "编辑账号资料", "套餐、日期、代理绑定"},
		{PermAccountsArchive, "归档 / 恢复账号", ""},
		{PermAccountsDelete, "删除未上号账号", "仅限从未被平台接收的账号，连同本地凭证一起删除"},
		{PermAccountsImport, "JSON 批量导入", ""},
		{PermAccountsCredential, "写入密码与 Claude 凭证", "涉及敏感信息"},
		{PermAccountsOnboard, "提交上号到供应商", "会向平台发起真实请求"},
		{PermAccountsUsage, "查看账号平台用量明细", ""},
		{PermAccountsPlatformEdit, "修改平台上的账号资料", "通过供应商网页后台写入，仅资料字段"},
	}},
	{"proxies", "代理池", "代理池", []permissionDef{
		{PermProxiesView, "查看代理与检测记录", ""},
		{PermProxiesCreate, "新增代理", ""},
		{PermProxiesUpdate, "修改代理", "名称、地址、认证、启用状态"},
		{PermProxiesArchive, "停用代理", ""},
		{PermProxiesDelete, "删除代理", "仅限未被账号绑定的代理"},
		{PermProxiesTest, "发起本机检测", ""},
		{PermProxiesSettings, "修改本机检测路径", "前置代理地址与认证"},
	}},
	{"jobs", "任务中心", "任务中心", []permissionDef{{PermJobsView, "查看任务记录", ""}}},
	{"bills", "平台账单", "平台离线账单", []permissionDef{
		{PermBillsView, "查看已同步账单与用量", ""},
		{PermBillsQuery, "向平台查询区间账单", "会消耗平台接口额度"},
		{PermBillsSync, "发起 / 重试账单同步", ""},
	}},
	{"connections", "平台设置", "平台设置", []permissionDef{
		{PermConnectionsView, "查看平台连接", "Token 永不回显"},
		{PermConnectionsCreate, "新建平台连接", ""},
		{PermConnectionsUpdate, "修改平台连接与 Token", ""},
		{PermConnectionsDelete, "删除平台连接", ""},
	}},
	{"audit", "审计日志", "审计日志", []permissionDef{{PermAuditView, "查看审计日志", ""}}},
	{"users", "用户管理", "用户与权限", []permissionDef{
		{PermUsersView, "查看用户", ""},
		{PermUsersCreate, "新建用户", ""},
		{PermUsersUpdate, "修改用户角色与状态", ""},
		{PermUsersDelete, "删除用户", ""},
		{PermUsersResetPassword, "重置他人密码", ""},
	}},
	{"roles", "角色管理", "", []permissionDef{
		{PermRolesView, "查看角色与权限目录", ""},
		{PermRolesManage, "新建 / 修改 / 删除角色", "可授予的权限不超过自身权限"},
	}},
}

var permissionIndex = func() map[Permission]bool {
	m := map[Permission]bool{}
	for _, mod := range permissionCatalogue {
		for _, p := range mod.Permissions {
			m[p.Key] = true
		}
	}
	return m
}()

func allPermissionKeys() []string {
	out := []string{}
	for _, mod := range permissionCatalogue {
		for _, p := range mod.Permissions {
			out = append(out, string(p.Key))
		}
	}
	return out
}

// viewPermissionKeys returns the read permissions of business modules; user
// and role administration are excluded so a read-only role cannot enumerate
// accounts or permission sets.
func viewPermissionKeys() []string {
	out := []string{}
	for _, mod := range permissionCatalogue {
		if mod.Key == "users" || mod.Key == "roles" {
			continue
		}
		for _, p := range mod.Permissions {
			if k := string(p.Key); len(k) > 5 && k[len(k)-5:] == ".view" {
				out = append(out, k)
			}
		}
	}
	return out
}

// Builtin roles are seeded once and keep stable ids. The administrator role
// is a superuser: it is never checked against the catalogue, so permissions
// added in later versions apply to it automatically.
const (
	roleAdminID    = "role-admin"
	roleOperatorID = "role-operator"
	roleViewerID   = "role-viewer"
)

type builtinRole struct {
	id, name, description string
	permissions           []string
}

func builtinRoles() []builtinRole {
	return []builtinRole{
		{roleAdminID, "管理员", "超级管理员，拥有全部权限，不可修改", allPermissionKeys()},
		{roleOperatorID, "运营", "账号录入、上号、代理与账单同步；不能修改平台连接和用户", []string{
			string(PermDashboardView),
			string(PermAccountsView), string(PermAccountsCreate), string(PermAccountsUpdate), string(PermAccountsArchive), string(PermAccountsDelete), string(PermAccountsImport), string(PermAccountsCredential), string(PermAccountsOnboard), string(PermAccountsUsage),
			string(PermProxiesView), string(PermProxiesCreate), string(PermProxiesUpdate), string(PermProxiesArchive), string(PermProxiesDelete), string(PermProxiesTest), string(PermProxiesSettings),
			string(PermJobsView),
			string(PermBillsView), string(PermBillsQuery), string(PermBillsSync),
			string(PermConnectionsView), string(PermAuditView),
		}},
		{roleViewerID, "只读", "只能查看页面与记录，不能执行任何修改", append(viewPermissionKeys(), string(PermAccountsUsage))},
	}
}
