package bugsmash

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMonthComments(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	c := func(id, status, created string) string {
		return fmt.Sprintf(`{"comment_id":%q,"comment":"x","comment_number":1,"status":%q,"created_at":%q}`, id, status, created)
	}
	for _, style := range []string{"cursor", "numbered"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			var body string
			switch q.Get("status") {
			case "active":
				body = `{"status":true,"data":[` + c("a1", "Active", "2026-10-03T10:00:00.000000Z") + `,` + c("a0", "Active", "2026-09-30T10:00:00.000000Z") + `]}`
			case "resolved":
				second := q.Get("cursor") == "next" || q.Get("page") == "2"
				switch {
				case !second && style == "cursor":
					body = `{"status":true,"data":[` + c("r1", "Resolved", "2026-10-07T18:20:21.000000Z") + `],"meta":{"nextCursor":"next"}}`
				case !second:
					body = `{"status":true,"current_page":1,"last_page":2,"data":[` + c("r1", "Resolved", "2026-10-07T18:20:21.000000Z") + `]}`
				default:
					body = `{"status":true,"current_page":2,"last_page":2,"data":[` + c("r2", "Resolved", "2026-10-20T10:00:00.000000Z") + `,` + c("r3", "Resolved", "2026-11-01T10:00:00.000000Z") + `],"meta":{"nextCursor":null}}`
				}
			}
			_, _ = w.Write([]byte(body))
		}))
		cl := &Client{BaseURL: srv.URL, Key: "k", HTTP: srv.Client()}
		rec, res, err := cl.MonthComments(context.Background(), "p", from, to)
		srv.Close()
		if err != nil || rec != 3 || res != 2 {
			t.Errorf("%s pages: received=%d resolved=%d err=%v", style, rec, res, err)
		}
	}
}
