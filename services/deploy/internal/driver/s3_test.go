package driver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeS3 — accepts any PUT, records key + body. Doesn't validate
// signatures (drift across implementations would make that brittle);
// presence of the Authorization header is asserted instead.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	gotAuth []string
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string][]byte{}}
}

func (f *fakeS3) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.objects[r.URL.Path] = body
		f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func TestS3DeployRoundtrip(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	d := NewS3(srv.URL, "us-east-1", "biumind",
		"AKIATEST", "secret", srv.URL, false)
	tar := makeTarball(t, map[string]string{
		"index.html":  "<h1>x</h1>",
		"assets/a.js": "console.log(1)",
	})
	dep, err := d.Deploy(context.Background(), Plan{
		OwnerID: "u1", Kind: KindStatic, Tarball: tar,
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.Status != "running" || !strings.Contains(dep.URL, "/biumind/"+dep.ID+"/") {
		t.Errorf("bad deployment: %+v", dep)
	}
	// Two objects should have landed.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.objects) != 2 {
		t.Fatalf("want 2 PUTs, got %d: %+v", len(fake.objects), keys(fake.objects))
	}
	gotIndex := false
	for k, body := range fake.objects {
		if strings.Contains(k, dep.ID) && strings.HasSuffix(k, "index.html") {
			gotIndex = true
			if string(body) != "<h1>x</h1>" {
				t.Errorf("body mismatch: %q", body)
			}
		}
	}
	if !gotIndex {
		t.Errorf("index.html PUT not seen")
	}
	for _, auth := range fake.gotAuth {
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIATEST/") {
			t.Errorf("bad auth header: %s", auth)
		}
	}
}

func TestS3RejectsPathEscape(t *testing.T) {
	fake := newFakeS3()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	d := NewS3(srv.URL, "us-east-1", "biumind", "AKIA", "s", srv.URL, false)
	tar := makeTarball(t, map[string]string{
		"../escape.txt": "x",
		"safe.txt":      "ok",
	})
	dep, _ := d.Deploy(context.Background(), Plan{
		OwnerID: "u1", Kind: KindStatic, Tarball: tar,
	})
	// Escape path should have been silently skipped; safe.txt landed.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.objects) != 1 {
		t.Errorf("want 1 PUT (escape rejected), got %d: %+v", len(fake.objects), keys(fake.objects))
	}
	for k := range fake.objects {
		if strings.Contains(k, "escape.txt") {
			t.Errorf("escape.txt should not have been uploaded")
		}
	}
	if dep.Status != "running" {
		t.Errorf("safe entries should still produce a running deployment, got %s", dep.Status)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
