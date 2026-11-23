package app

import (
	"net/http"
	"strings"
)

// Handler wires the HTTP API. Routes use Go 1.22 method patterns; handlers
// that serve several methods on one path dispatch internally so mismatches
// still produce the JSON error envelope.
func (a *App) Handler() http.Handler {
	api := http.NewServeMux()
	route := func(pattern string, h http.HandlerFunc) { api.HandleFunc(pattern, a.protect(h)) }
	// The OAuth write guard sits inside the permission check: a caller without
	// the permission must not be able to probe account locks or learn that an
	// account is mid-authorization from a 409.
	guard := a.guardOAuthWrites
	need := func(pattern string, p Permission, h http.HandlerFunc) { route(pattern, a.require(p, guard(h))) }
	needFn := func(pattern string, pick func(*http.Request) Permission, h http.HandlerFunc) {
		route(pattern, a.requireFn(pick, guard(h)))
	}
	needAny := func(pattern string, h http.HandlerFunc, ps ...Permission) { route(pattern, a.requireAny(ps, guard(h))) }

	api.HandleFunc("GET /api/v1/auth/session", a.authSession)
	api.HandleFunc("POST /api/v1/auth/login", a.authLogin)
	route("POST /api/v1/auth/logout", a.authLogout)

	// 用户之声: every signed-in user may submit and follow their own feedback.
	route("POST /api/v1/feedback", a.feedbackCreate)
	route("GET /api/v1/feedback", a.feedbackList)
	route("GET /api/v1/feedback/summary", a.feedbackSummary)
	need("PATCH /api/v1/feedback/{id}", PermFeedbackManage, a.feedbackUpdate)

	// Site-wide announcement: every signed-in user can read; publish needs manage.
	route("GET /api/v1/announcement", a.announcementGet)
	need("PUT /api/v1/announcement", PermAnnouncementManage, a.announcementPut)

	need("GET /api/v1/dashboard", PermDashboardView, a.dashboard)
	need("GET /api/v1/dashboard/attention", PermDashboardView, a.dashboardAttention)
	need("GET /api/v1/dashboard/insights", PermDashboardView, a.dashboardInsights)
	needOnboard := func(pattern string, p Permission, h http.HandlerFunc) {
		route(pattern, a.require(p, a.requireCanOnboard(guard(h))))
	}
	needOnboard("POST /api/v1/claude-oauth/sessions", PermAccountsOnboard, a.claudeOAuthStart)
	needOnboard("POST /api/v1/claude-oauth/sessions/{id}/exchange", PermAccountsOnboard, a.claudeOAuthExchange)
	needOnboard("DELETE /api/v1/claude-oauth/sessions/{id}", PermAccountsOnboard, a.claudeOAuthCancel)

	needFn("/api/v1/accounts", byMethod(PermAccountsView, map[string]Permission{"POST": PermAccountsCreate}), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			a.requireCanOnboard(a.accounts)(w, r)
			return
		}
		a.accounts(w, r)
	})
	needOnboard("POST /api/v1/accounts/onboard", PermAccountsOnboard, func(w http.ResponseWriter, r *http.Request) { a.onboardAccount(w, r) })
	need("GET /api/v1/accounts/{id}", PermAccountsView, a.accountGet)
	need("PATCH /api/v1/accounts/{id}", PermAccountsUpdate, a.accountPatch)
	need("POST /api/v1/accounts/{id}/assign", PermAccountsUpdate, a.accountAssign)
	need("POST /api/v1/accounts/{id}/platform-profile", PermAccountsPlatformEdit, a.platformProfileUpdate)
	needOnboard("POST /api/v1/accounts/{id}/onboard", PermAccountsOnboard, func(w http.ResponseWriter, r *http.Request) { a.onboardAccount(w, r, r.PathValue("id")) })
	need("DELETE /api/v1/accounts/{id}", PermAccountsDelete, a.accountDelete)
	need("POST /api/v1/accounts/{id}/archive", PermAccountsArchive, a.accountArchive(true))
	need("POST /api/v1/accounts/{id}/restore", PermAccountsArchive, a.accountArchive(false))
	need("PUT /api/v1/accounts/{id}/credentials", PermAccountsCredential, a.accountCredentials)
	need("PATCH /api/v1/accounts/{id}/credentials", PermAccountsCredential, a.accountCredentials)
	need("POST /api/v1/accounts/{id}/reauthorize", PermAccountsCredential, a.accountReauthorize)
	need("POST /api/v1/accounts/{id}/preflight", PermAccountsView, a.accountPreflight)
	need("GET /api/v1/accounts/{id}/platform-usage", PermAccountsUsage, a.accountPlatformUsage)
	need("GET /api/v1/accounts/{id}/platform-usage-history", PermAccountsUsage, a.accountUsageHistory)
	needOnboard("POST /api/v1/accounts/{id}/submit", PermAccountsOnboard, func(w http.ResponseWriter, r *http.Request) { a.submitExistingAccount(w, r, r.PathValue("id")) })

	needFn("/api/v1/proxy-settings", byMethod(PermProxiesView, map[string]Permission{"PUT": PermProxiesSettings}), a.proxyNetworkSettings)
	needFn("/api/v1/oauth-settings", byMethod(PermOAuthSettingsView, map[string]Permission{"PUT": PermOAuthSettings}), a.oauthDispatchSettingsHandler)
	needFn("/api/v1/proxies", byMethod(PermProxiesView, map[string]Permission{"POST": PermProxiesCreate}), a.proxies)
	need("PATCH /api/v1/proxies/{id}", PermProxiesUpdate, a.proxyUpdate)
	need("POST /api/v1/proxies/{id}/assign", PermProxiesUpdate, a.proxyAssign)
	need("DELETE /api/v1/proxies/{id}", PermProxiesDelete, a.proxyDelete)
	need("POST /api/v1/proxies/{id}/archive", PermProxiesArchive, a.proxyArchive)
	// Lookups expose only names and ids so account and bill screens can offer
	// pickers without granting the proxies/connections modules themselves.
	needAny("GET /api/v1/lookups/proxies", a.lookupProxies, PermAccountsView, PermAccountsOnboard)
	needAny("GET /api/v1/lookups/platform-connections", a.lookupConnections, PermAccountsView, PermBillsView)
	need("POST /api/v1/proxies/{id}/test", PermProxiesTest, a.proxyTest)
	need("GET /api/v1/proxies/{id}/checks", PermProxiesView, func(w http.ResponseWriter, r *http.Request) { a.proxyChecks(w, r, r.PathValue("id")) })

	needOnboard("POST /api/v1/imports/preview", PermAccountsImport, a.importPreview)
	needOnboard("POST /api/v1/imports/{id}/commit", PermAccountsImport, func(w http.ResponseWriter, r *http.Request) { a.importCommit(w, r, r.PathValue("id")) })

	needFn("/api/v1/platform-connections", byMethod(PermConnectionsView, map[string]Permission{"POST": PermConnectionsCreate}), a.platformConnections)
	needFn("/api/v1/platform-connections/{id}", byMethod(PermConnectionsView, map[string]Permission{"PUT": PermConnectionsUpdate, "PATCH": PermConnectionsUpdate, "DELETE": PermConnectionsDelete}), a.platformConnectionDetail)
	need("POST /api/v1/platform-connections/{id}/web-login-test", PermConnectionsUpdate, a.webLoginTest)
	need("GET /api/v1/platform-connections/{id}/remote-accounts", PermAccountsView, a.remoteAccounts)

	need("GET /api/v1/jobs", PermJobsView, a.jobs)
	need("GET /api/v1/audit-events", PermAuditView, a.auditEvents)
	platformUsage := func(w http.ResponseWriter, r *http.Request) {
		a.platformUsage(w, r, strings.TrimPrefix(r.URL.Path, "/api/v1/platform-usage"))
	}
	needFn("/api/v1/platform-usage", billPermission, platformUsage)
	needFn("/api/v1/platform-usage/", billPermission, platformUsage)
	route("POST /api/v1/auth/password", a.changePassword)
	need("GET /api/v1/users", PermUsersView, a.listUsers)
	need("POST /api/v1/users", PermUsersCreate, a.createUser)
	need("PATCH /api/v1/users/{id}", PermUsersUpdate, a.updateUser)
	need("DELETE /api/v1/users/{id}", PermUsersDelete, a.deleteUser)
	need("GET /api/v1/permissions", PermRolesView, a.permissionCatalogueHandler)
	need("GET /api/v1/roles", PermRolesView, a.listRoles)
	need("POST /api/v1/roles", PermRolesManage, a.createRole)
	need("PATCH /api/v1/roles/{id}", PermRolesManage, a.updateRole)
	need("DELETE /api/v1/roles/{id}", PermRolesManage, a.deleteRole)
	api.HandleFunc("/api/v1/", notFound)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", api)
	mux.HandleFunc("/healthz", a.healthz)
	mux.HandleFunc("GET /metrics", a.metrics)
	mux.Handle("/", spaHandler(a.cfg.WebDir))
	return a.observe(mux)
}

// billPermission distinguishes read, supplier interval queries and sync
// writes under /platform-usage.
func billPermission(r *http.Request) Permission {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return PermBillsView
	}
	if strings.HasSuffix(r.URL.Path, "/bills/summary") || strings.HasSuffix(r.URL.Path, "/bills/query") {
		return PermBillsQuery
	}
	return PermBillsSync
}
