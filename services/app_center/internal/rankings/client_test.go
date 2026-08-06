package rankings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_Fetch_NumericID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "weibo" {
			t.Errorf("expected id=weibo, got %s", r.URL.Query().Get("id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","id":"weibo","updatedTime":1700000000000,"items":[
		  {"id":12345,"title":"hot 1","url":"https://s.weibo.com/x"},
		  {"id":"abc","title":"hot 2","url":"https://s.weibo.com/y"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	snap, err := c.Fetch(context.Background(), "weibo")
	if err != nil {
		t.Fatal(err)
	}
	if snap.BoardID != "weibo" {
		t.Errorf("BoardID = %q", snap.BoardID)
	}
	if snap.UpdatedTime != 1700000000000 {
		t.Errorf("UpdatedTime = %d", snap.UpdatedTime)
	}
	if len(snap.Items) != 2 {
		t.Fatalf("items = %d", len(snap.Items))
	}
	if snap.Items[0].ID != "12345" {
		t.Errorf("numeric id not stringified: %q", snap.Items[0].ID)
	}
	if snap.Items[1].ID != "abc" {
		t.Errorf("string id not preserved: %q", snap.Items[1].ID)
	}
}

func TestClient_Fetch_ExtraFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"zhihu","items":[
		  {"id":"q1","title":"x","url":"https://www.zhihu.com/q/1","extra":{"info":"123 万热度","hover":"[视频]"}}
		]}`))
	}))
	defer srv.Close()

	snap, _ := NewClient(srv.URL).Fetch(context.Background(), "zhihu")
	if got := snap.Items[0].Extra["info"]; got != "123 万热度" {
		t.Errorf("extra.info = %v", got)
	}
}

func TestClient_Fetch_FallsBackToURLWhenIDMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"baidu","items":[
		  {"title":"only title","url":"https://www.baidu.com/s?wd=x"}
		]}`))
	}))
	defer srv.Close()

	snap, _ := NewClient(srv.URL).Fetch(context.Background(), "baidu")
	if snap.Items[0].ID != "https://www.baidu.com/s?wd=x" {
		t.Errorf("id fallback wrong: %q", snap.Items[0].ID)
	}
}

func TestClient_Fetch_DropsEmptyTitleItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","items":[
		  {"title":"keep","url":"https://x.com/1"},
		  {"id":"empty","url":"https://x.com/2"}
		]}`))
	}))
	defer srv.Close()
	snap, _ := NewClient(srv.URL).Fetch(context.Background(), "x")
	if len(snap.Items) != 1 || snap.Items[0].Title != "keep" {
		t.Errorf("expected only 'keep', got %+v", snap.Items)
	}
}

func TestClient_Fetch_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).Fetch(context.Background(), "x")
	if !errors.Is(err, ErrUpstreamFailed) {
		t.Errorf("expected ErrUpstreamFailed, got %v", err)
	}
}

func TestClient_Fetch_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).Fetch(context.Background(), "x")
	if !errors.Is(err, ErrUpstreamShape) {
		t.Errorf("expected ErrUpstreamShape, got %v", err)
	}
}

func TestClient_Fetch_IDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"weibo","items":[]}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).Fetch(context.Background(), "zhihu")
	if !errors.Is(err, ErrUpstreamShape) || !strings.Contains(err.Error(), "weibo") {
		t.Errorf("expected shape-mismatch with id, got %v", err)
	}
}

func TestValidateSnapshot_AllowsExactAndSubdomain(t *testing.T) {
	cases := []struct {
		name       string
		snap       *Snapshot
		domain     string
		wantOK     bool
		wantSubstr string
	}{
		{
			name: "exact host",
			snap: &Snapshot{Items: []Item{{Title: "x", URL: "https://baidu.com/s"}}},
			domain: "baidu.com", wantOK: true,
		},
		{
			name: "subdomain",
			snap: &Snapshot{Items: []Item{{Title: "x", URL: "https://www.baidu.com/s"}}},
			domain: "baidu.com", wantOK: true,
		},
		{
			name: "wrong host",
			snap: &Snapshot{Items: []Item{{Title: "x", URL: "https://evil.com/s"}}},
			domain: "baidu.com", wantOK: false, wantSubstr: "evil.com",
		},
		{
			name: "http rejected",
			snap: &Snapshot{Items: []Item{{Title: "x", URL: "http://baidu.com/s"}}},
			domain: "baidu.com", wantOK: false, wantSubstr: "non-https",
		},
		{
			name:   "empty domain skips",
			snap:   &Snapshot{Items: []Item{{Title: "x", URL: "http://anywhere.com"}}},
			domain: "", wantOK: true,
		},
		{
			name:   "empty url skipped silently",
			snap:   &Snapshot{Items: []Item{{Title: "x", URL: ""}}},
			domain: "baidu.com", wantOK: true,
		},
		{
			name: "lookalike domain",
			snap: &Snapshot{Items: []Item{{Title: "x", URL: "https://baidu.com.evil/s"}}},
			domain: "baidu.com", wantOK: false, wantSubstr: "baidu.com.evil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSnapshot(tc.snap, tc.domain)
			if tc.wantOK && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected error, got nil")
			}
			if tc.wantSubstr != "" && err != nil && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q missing %q", err, tc.wantSubstr)
			}
		})
	}
}
