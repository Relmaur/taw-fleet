package pagespeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") == "spent" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded"}}`))
			return
		}
		if q.Get("url") != "https://lsmexico.mx" || len(q["category"]) != 4 || q.Get("strategy") != "mobile" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"lighthouseResult":{"categories":{"performance":{"score":0.915},"accessibility":{"score":0.97},"best-practices":{"score":1},"seo":{"score":null}},
			"audits":{"largest-contentful-paint":{"displayValue":"2.1 s"},"cumulative-layout-shift":{"displayValue":"0.01"},"total-blocking-time":{"displayValue":"40 ms"}}},
			"loadingExperience":{"overall_category":"FAST","metrics":{"LARGEST_CONTENTFUL_PAINT_MS":{"percentile":1900},"INTERACTION_TO_NEXT_PAINT":{"percentile":120},"CUMULATIVE_LAYOUT_SHIFT_SCORE":{"percentile":2}}}}`))
	}))
	defer srv.Close()
	c := Client{Endpoint: srv.URL, HTTP: srv.Client()}
	r, err := c.Run(context.Background(), "https://lsmexico.mx", "mobile")
	if err != nil {
		t.Fatal(err)
	}
	if r.Performance != 92 || r.Accessibility != 97 || r.BestPractices != 100 || r.SEO != -1 || r.LCP != "2.1 s" {
		t.Errorf("scores %+v", r)
	}
	if r.Field == nil || r.Field.LCPms != 1900 || r.Field.INPms != 120 || r.Field.CLS != 0.02 || r.Field.Overall != "FAST" {
		t.Errorf("field %+v", r.Field)
	}
	c.Key = "spent"
	if _, err := c.Run(context.Background(), "https://lsmexico.mx", "mobile"); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Errorf("429: %v", err)
	}
}
