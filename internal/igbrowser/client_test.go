package igbrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRunsPageFetch(t *testing.T) {
	var gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotBody = payload["url"]
		_, _ = w.Write([]byte(`{"status":200,"body":"{\"ok\":true}"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Fetch(context.Background(), "https://www.instagram.com/graphql/query", "POST", "a=1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotPath != "/fetch" {
		t.Errorf("sidecar path = %s", gotPath)
	}
	if gotBody != "https://www.instagram.com/graphql/query" {
		t.Errorf("passed url = %q", gotBody)
	}
	if res.Status != 200 || res.Body != `{"ok":true}` {
		t.Errorf("result = %+v", res)
	}
}

func TestImportCookies(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		got = string(raw)
		_, _ = w.Write([]byte(`{"imported":2}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.ImportCookies(context.Background(), "# Netscape\n.instagram.com\tTRUE\t/\tTRUE\t1\tmid\tx\n"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(got) == 0 {
		t.Error("netscape body was not sent")
	}
}

func TestStatusParsesSidecar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/status" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ready":true,"session":"ok","cookies":9,"ds_user_id":"123"}`))
	}))
	defer srv.Close()

	st, err := New(srv.URL).Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Session != "ok" || st.Cookies != 9 || st.DsUserID != "123" {
		t.Errorf("status = %+v", st)
	}
}

func TestSidecarErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "browser not ready", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Fetch(context.Background(), "https://www.instagram.com/x", "GET", ""); err == nil {
		t.Fatal("want error on sidecar failure")
	}
}
