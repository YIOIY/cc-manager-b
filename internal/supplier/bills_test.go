package supplier

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBillCalendarWindow(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	fixed := time.Date(2026, 11, 5, 23, 59, 59, 0, loc)
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{{"2026-08-07", "2026-11-05", true}, {"20260807", "20261105", true}, {"2026-08-06", "2026-11-05", false}, {"2026-11-06", "2026-11-06", false}, {"2026-11-05", "2026-11-04", false}, {"2026-02-30", "2026-11-05", false}} {
		_, err := ValidateBillDates(tc.from, tc.to, loc, fixed.UTC(), true)
		if (err == nil) != tc.valid {
			t.Errorf("%+v error=%v", tc, err)
		}
	}
	if _, err := ValidateBillDates("2020-01-01", "2020-01-02", nil, time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	newYork, _ := time.LoadLocation("America/New_York")
	dst := time.Date(2026, 11, 5, 12, 0, 0, 0, newYork)
	boundary := dst.AddDate(0, 0, -90).Format("2006-01-02")
	if _, err := ValidateBillDates(boundary, "2026-11-05", newYork, dst, true); err != nil {
		t.Fatal(err)
	}
}

func TestBillResponseValidation(t *testing.T) {
	row := `{"id":12345,"request_count":1,"prompt_tokens":2,"completion_tokens":3,"cache_read_input_tokens":4,"cache_write_input_tokens":5,"cost":0.01}`
	for _, tc := range []struct {
		name, response string
		wantErr        string
		valid, invalid int
	}{
		{"numeric range", `{"list":[` + row + `],"range":{"start_ds":20261105,"end_ds":20261105}}`, "", 1, 0},
		{"missing range", `{"list":[` + row + `]}`, "BILL_RANGE_INVALID", 0, 0},
		{"different range", `{"list":[` + row + `],"range":{"start_ds":"2026-11-04","end_ds":"2026-11-05"}}`, "BILL_RANGE_MISMATCH", 0, 0},
		{"missing list", `{"range":{"start_ds":20261105,"end_ds":20261105}}`, "BILL_RESPONSE_INVALID", 0, 0},
		{"empty list", `{"list":[],"range":{"start_ds":20261105,"end_ds":20261105}}`, "", 0, 0},
		{"duplicate", `{"list":[` + row + `,` + row + `],"range":{"start_ds":20261105,"end_ds":20261105}}`, "BILL_ID_MISMATCH", 0, 0},
		{"unknown account", `{"list":[{"id":999}],"range":{"start_ds":20261105,"end_ds":20261105}}`, "BILL_ID_MISMATCH", 0, 0},
		{"null fields", `{"list":[{"id":12345,"cost":0}],"range":{"start_ds":20261105,"end_ds":20261105}}`, "", 0, 1},
		{"partial row error", `{"list":[` + row + `,{"id":12346,"cost":null}],"range":{"start_ds":20261105,"end_ds":20261105}}`, "", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.response) }))
			defer server.Close()
			client, err := New(server.URL, "fixture-token")
			if err != nil {
				t.Fatal(err)
			}
			client.http = server.Client()
			result, err := client.QueryBillResult(context.Background(), []string{"12345", "12346"}, "2026-11-05", "2026-11-05")
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error=%v want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Bills) != tc.valid || len(result.RowErrors) != tc.invalid {
				t.Fatalf("result=%+v", result)
			}
		})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "2"); w.WriteHeader(429) }))
	defer server.Close()
	client, _ := New(server.URL, "fixture-token")
	client.http = server.Client()
	_, err := client.QueryBillResult(context.Background(), []string{"12345"}, "2026-11-05", "2026-11-05")
	httpErr, ok := err.(*BillHTTPError)
	if !ok || httpErr.Status != 429 || httpErr.RetryAfter != 2*time.Second {
		t.Fatalf("error=%v", err)
	}
}
