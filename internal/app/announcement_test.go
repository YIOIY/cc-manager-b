package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAnnouncementPublishAndClear(t *testing.T) {
	a, admin := rbacApp(t)

	var empty struct {
		Data struct {
			Body      string `json:"body"`
			CanManage bool   `json:"can_manage"`
		} `json:"data"`
	}
	if res := admin.do(http.MethodGet, "/api/v1/announcement", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("get empty status=%d", res.StatusCode)
	} else {
		_ = jsonDecode(res, &empty)
		if empty.Data.Body != "" || !empty.Data.CanManage {
			t.Fatalf("empty announcement=%+v", empty.Data)
		}
	}

	text := "系统维护通知：今晚 22:00 短暂停机"
	if res := admin.do(http.MethodPut, "/api/v1/announcement", `{"body":`+jsonString(text)+`}`); res.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d", res.StatusCode)
	}

	var got struct {
		Data struct {
			Body      string `json:"body"`
			UpdatedBy string `json:"updated_by"`
		} `json:"data"`
	}
	if res := admin.do(http.MethodGet, "/api/v1/announcement", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("get published status=%d", res.StatusCode)
	} else {
		_ = jsonDecode(res, &got)
		if got.Data.Body != text || got.Data.UpdatedBy != "admin" {
			t.Fatalf("published=%+v", got.Data)
		}
	}

	if res := admin.do(http.MethodPost, "/api/v1/users", `{"username":"ro","password":"viewer-password-1","role_ids":["`+roleViewerID+`"]}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer status=%d", res.StatusCode)
	}
	viewer := newTestClient(t, a)
	viewer.loginAs("ro", "viewer-password-1")
	var viewerGot struct {
		Data struct {
			Body      string `json:"body"`
			CanManage bool   `json:"can_manage"`
		} `json:"data"`
	}
	if res := viewer.do(http.MethodGet, "/api/v1/announcement", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("viewer get status=%d", res.StatusCode)
	} else {
		_ = jsonDecode(res, &viewerGot)
		if viewerGot.Data.Body != text || viewerGot.Data.CanManage {
			t.Fatalf("viewer announcement=%+v", viewerGot.Data)
		}
	}
	if res := viewer.do(http.MethodPut, "/api/v1/announcement", `{"body":"hack"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer put status=%d", res.StatusCode)
	}

	long := strings.Repeat("公告", 251)
	if res := admin.do(http.MethodPut, "/api/v1/announcement", `{"body":`+jsonString(long)+`}`); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("too long status=%d", res.StatusCode)
	}

	if res := admin.do(http.MethodPut, "/api/v1/announcement", `{"body":""}`); res.StatusCode != http.StatusOK {
		t.Fatalf("clear status=%d", res.StatusCode)
	}
	if res := admin.do(http.MethodGet, "/api/v1/announcement", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("get cleared status=%d", res.StatusCode)
	} else {
		_ = jsonDecode(res, &got)
		if got.Data.Body != "" {
			t.Fatalf("cleared body=%q", got.Data.Body)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
