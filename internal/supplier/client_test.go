package supplier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestQueryBillsDecodesSupplierFields(t *testing.T) {
	day := time.Now().Format("2006-01-02")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/supplier-account/api/openapi/bill/query" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-openapi-token" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing supplier authorization or JSON content type")
			http.Error(w, "invalid headers", http.StatusBadRequest)
			return
		}
		var body struct {
			AccountIDs []int64 `json:"account_ids"`
			Start      string  `json:"start_ds"`
			End        string  `json:"end_ds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if !reflect.DeepEqual(body.AccountIDs, []int64{12345, 12346}) || body.Start != day || body.End != day {
			t.Errorf("unexpected bill query: %+v", body)
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"list":[
			{"id":12345,"request_count":1234,"prompt_tokens":4200000000,"completion_tokens":50000,"cache_read_input_tokens":3000,"cache_write_input_tokens":1000,"cost":12.34},
			{"id":12346,"request_count":0,"prompt_tokens":0,"completion_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0,"cost":"0.01"}
		],"range":{"start_ds":"`+day+`","end_ds":"`+day+`"}}`)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-openapi-token")
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	got, err := client.QueryBills(context.Background(), []string{"12345", "12346"}, day, day)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bill{
		{ID: 12345, RequestCount: 1234, PromptTokens: 4200000000, CompletionTokens: 50000, CacheReadInputTokens: 3000, CacheWriteInputTokens: 1000, CostFen: 1234},
		{ID: 12346, CostFen: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryBills() = %+v, want %+v", got, want)
	}
}
