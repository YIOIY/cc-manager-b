package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
)

func TestLocalAccountAndProxyFlow(t *testing.T) {
	db := t.TempDir() + "/test.db"
	t.Setenv("CC_MANAGER_ADMIN_PASSWORD", "secret-password")
	a, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	s := httptest.NewServer(a.Handler())
	defer s.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	res, _ := client.Get(s.URL + "/api/v1/auth/session")
	var sessionResp struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&sessionResp)
	res.Body.Close()
	res, _ = client.Get(s.URL + "/api/v1/auth/captcha")
	var captchaResp struct {
		Data struct {
			ID string `json:"captcha_id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&captchaResp)
	res.Body.Close()
	answer, ok := a.captchas.peek(captchaResp.Data.ID)
	if !ok {
		t.Fatal("missing captcha answer")
	}
	loginPayload, _ := json.Marshal(map[string]string{
		"username": "admin", "password": "secret-password",
		"captcha_id": captchaResp.Data.ID, "captcha": answer,
	})
	loginReq, _ := http.NewRequest(http.MethodPost, s.URL+"/api/v1/auth/login", bytes.NewReader(loginPayload))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://"+loginReq.Host)
	loginReq.Header.Set("X-CSRF-Token", sessionResp.Data.CSRF)
	res, err = client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&sessionResp)
	res.Body.Close()
	if len(jar.Cookies(loginReq.URL)) == 0 {
		t.Fatal("missing session cookie")
	}

	accountBody := bytes.NewBufferString(`{"account":"test@example.com","plan":"claude/cc-max-20x"}`)
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/api/v1/accounts", accountBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", sessionResp.Data.CSRF)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("account status=%d", res.StatusCode)
	}
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if envelope.Data.ID == "" {
		t.Fatal("missing account id")
	}

	get, _ := http.NewRequest(http.MethodGet, s.URL+"/api/v1/accounts", nil)
	res, err = client.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d", res.StatusCode)
	}
	res.Body.Close()

	proxyBody := bytes.NewBufferString(`{"name":"local","scheme":"http","host":"127.0.0.1","port":8080}`)
	proxyReq, _ := http.NewRequest(http.MethodPost, s.URL+"/api/v1/proxies", proxyBody)
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("X-CSRF-Token", sessionResp.Data.CSRF)
	res, err = client.Do(proxyReq)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("proxy status=%d", res.StatusCode)
	}
	res.Body.Close()
}
