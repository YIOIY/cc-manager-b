package app

import "net/http"

// lookupProxies lists proxies as picker options: no check history, no
// credentials. Disabled entries are included so existing bindings still
// resolve to a name.
func (a *App) lookupProxies(w http.ResponseWriter, r *http.Request) {
	scope := ownershipFrom(r)
	query := `SELECT id,name,scheme,host,port,enabled FROM proxies`
	args := []any{}
	if !scope.Admin {
		query += ` WHERE created_by_user_id=?`
		args = append(args, scope.UserID)
	}
	query += ` ORDER BY enabled DESC,name,host,rowid`
	rows, err := a.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, storageError(err, "代理读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, scheme, host string
		var port, enabled int
		if err = rows.Scan(&id, &name, &scheme, &host, &port, &enabled); err != nil {
			writeError(w, r, storageError(err, "代理读取失败"))
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "scheme": scheme, "host": host, "port": port, "enabled": enabled == 1})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "代理读取失败"))
		return
	}
	write(w, http.StatusOK, out)
}

// lookupConnections lists platform connections as picker options. The token
// is never part of the list; base_url is only a display fallback.
func (a *App) lookupConnections(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,name,base_url,timezone,enabled,web_username<>'' AND web_password_secret<>'' FROM platform_connections ORDER BY created_at,rowid`)
	if err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, name, base, zone string
		var enabled, webConfigured int
		if err = rows.Scan(&id, &name, &base, &zone, &enabled, &webConfigured); err != nil {
			writeError(w, r, storageError(err, "平台连接读取失败"))
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "base_url": base, "timezone": zone, "enabled": enabled == 1, "web_configured": webConfigured == 1})
	}
	if err = rows.Err(); err != nil {
		writeError(w, r, storageError(err, "平台连接读取失败"))
		return
	}
	write(w, http.StatusOK, out)
}
