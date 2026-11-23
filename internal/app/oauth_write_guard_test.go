package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The provider accepted the credential but the local CAS lost: the response is
// a 409 VERSION_CONFLICT whose registry wording cannot carry the "platform
// already updated" sentence, so the guard must rely on the structured marker.
// Without it the session stayed ready and the next submit pushed the same
// token a second time.
func TestOAuthProofRetiresSessionAfterPlatformUpdateWithLocalConflict(t *testing.T) {
	a, stub, admin := routesFixture(t)
	id := queryString(t, a, `SELECT id FROM accounts WHERE account='one@example.test'`)
	retryOAuthProxy(t, a, fixtureProvider("one@example.test"))

	started := startSession(t, admin, `{"proxy_id":"review-proxy","account_id":"`+id+`"}`)
	if started.status != http.StatusCreated {
		t.Fatalf("start: %d %v", started.status, started.data)
	}
	sessionID := started.data["id"].(string)
	u, _ := url.Parse(started.data["authorization_url"].(string))
	res := admin.do("POST", "/api/v1/claude-oauth/sessions/"+sessionID+"/exchange", `{"result":"code#`+u.Query().Get("state")+`"}`)
	exBody := readBody(res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("exchange: %d %s", res.StatusCode, exBody)
	}
	var ex struct {
		Data struct {
			Auth map[string]any `json:"auth_json"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(exBody), &ex)

	// The provider accepts the push and, meanwhile, the account row moves on.
	stub.updateResponse = func(w http.ResponseWriter) {
		if _, err := a.DB.Exec(`UPDATE accounts SET version=version+1 WHERE id=?`, id); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"id":428802,"msg":"修改成功"}`)
	}
	body, _ := json.Marshal(map[string]any{"auth_json": ex.Data.Auth, "oauth_session_id": sessionID, "proxy_id": "review-proxy"})
	first := admin.do("POST", "/api/v1/accounts/"+id+"/reauthorize", string(body))
	firstBody := readBody(first)
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(firstBody), &env)
	if first.StatusCode != http.StatusConflict || env.Error.Code != "VERSION_CONFLICT" || env.Error.Details["platform_updated"] != true {
		t.Fatalf("partial outcome not marked: %d %s", first.StatusCode, firstBody)
	}
	if len(stub.updates) != 1 {
		t.Fatalf("updates after first submit=%d", len(stub.updates))
	}
	a.oauth.mu.Lock()
	p, ok := a.oauth.items[sessionID]
	a.oauth.mu.Unlock()
	if !ok || p.completed == nil || p.ready || p.tokens != nil || p.lease != nil {
		t.Fatalf("session not retired after platform update: ok=%v completed=%v ready=%v tokens=%v lease=%v", ok, p.completed != nil, p.ready, p.tokens != nil, p.lease != nil)
	}

	// Same request replays the recorded outcome; a different one is refused.
	// Neither reaches the provider again.
	stub.updateResponse = nil
	replay := admin.do("POST", "/api/v1/accounts/"+id+"/reauthorize", string(body))
	if replayBody := readBody(replay); replay.StatusCode != http.StatusConflict || !strings.Contains(replayBody, `"platform_updated":true`) {
		t.Fatalf("replay: %d %s", replay.StatusCode, replayBody)
	}
	changed, _ := json.Marshal(map[string]any{"auth_json": ex.Data.Auth, "oauth_session_id": sessionID, "proxy_id": "review-proxy", "expected_version": 99})
	refused := admin.do("POST", "/api/v1/accounts/"+id+"/reauthorize", string(changed))
	if refusedBody := readBody(refused); refused.StatusCode != http.StatusConflict || !strings.Contains(refusedBody, "OAUTH_SESSION_CONFLICT") {
		t.Fatalf("rewrite after completion: %d %s", refused.StatusCode, refusedBody)
	}
	if len(stub.updates) != 1 {
		t.Fatalf("authorization pushed %d times", len(stub.updates))
	}
}
