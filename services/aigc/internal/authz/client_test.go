package authz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHTTP_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/authz/check" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req checkReq
		_ = json.Unmarshal(body, &req)
		if req.Action != ActionCreateTask {
			t.Errorf("action = %s", req.Action)
		}
		_, _ = w.Write([]byte(`{"decision":"ALLOW","reason":"owner"}`))
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL)
	uid := uuid.New()
	r, err := c.Check(context.Background(), Request{
		Principal: PrincipalUser(uid, "pro", "user"),
		Action:    ActionCreateTask,
		Resource:  ResourceModelByCode("wanx-2.6-t2i"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Allowed() {
		t.Errorf("decision = %s", r.Decision)
	}
}

func TestHTTP_Deny(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"DENY","reason":"not owner"}`))
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL)
	r, err := c.Check(context.Background(), Request{
		Principal: PrincipalUser(uuid.New(), "free", ""),
		Action:    ActionDeleteTask,
		Resource:  ResourceTask(uuid.New(), uuid.New(), false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Allowed() {
		t.Errorf("should be denied")
	}
	if !strings.Contains(r.Reason, "not owner") {
		t.Errorf("reason = %s", r.Reason)
	}
}

func TestHTTP_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL)
	_, err := c.Check(context.Background(), Request{
		Principal: PrincipalUser(uuid.New(), "", ""),
		Action:    ActionListMyTasks,
		Resource:  Entity{Type: "User", ID: "self"},
	})
	if err == nil {
		t.Fatal("want error on 5xx")
	}
}

func TestHTTP_NoURL(t *testing.T) {
	c := &HTTP{} // 空 URL
	_, err := c.Check(context.Background(), Request{
		Principal: PrincipalUser(uuid.New(), "", ""),
		Action:    ActionCreateTask,
	})
	if err == nil {
		t.Fatal("want error when URL empty")
	}
}

func TestAllow_Helper(t *testing.T) {
	allowed, err := Authorize(context.Background(), AlwaysAllow{},
		PrincipalUser(uuid.New(), "pro", ""),
		ActionCreateTask,
		ResourceModelByCode("wanx-2.6-t2i"))
	if err != nil || !allowed {
		t.Errorf("AlwaysAllow: allowed=%v err=%v", allowed, err)
	}

	allowed, err = Authorize(context.Background(), AlwaysDeny{},
		PrincipalUser(uuid.New(), "free", ""),
		ActionDeleteTask,
		ResourceTask(uuid.New(), uuid.New(), false))
	if err != nil || allowed {
		t.Errorf("AlwaysDeny: allowed=%v err=%v", allowed, err)
	}
}

func TestAllow_FailClosed(t *testing.T) {
	// HTTP error → Allow 返回 (false, err); 调用方负责 fail-closed.
	c := &HTTP{} // 无 URL → error
	allowed, err := Authorize(context.Background(), c,
		PrincipalUser(uuid.New(), "", ""),
		ActionCreateTask,
		ResourceModelByCode("any"))
	if err == nil {
		t.Fatal("want error")
	}
	if allowed {
		t.Error("must be deny on error (fail-closed)")
	}
	// errors 库识别原样
	if errors.Is(err, errors.New("nonexistent")) {
		// just for the import; ignore
	}
}

func TestPrincipalUser_Attributes(t *testing.T) {
	uid := uuid.New()
	p := PrincipalUser(uid, "team", "admin")
	if p.Type != EntityUser || p.ID != uid.String() {
		t.Errorf("type/id mismatch: %+v", p)
	}
	if p.Attributes["plan"] != "team" || p.Attributes["role"] != "admin" {
		t.Errorf("attrs = %+v", p.Attributes)
	}

	// 空 plan/role 不应出现在 attrs
	p2 := PrincipalUser(uid, "", "")
	if _, ok := p2.Attributes["plan"]; ok {
		t.Error("empty plan should not appear")
	}
	if _, ok := p2.Attributes["role"]; ok {
		t.Error("empty role should not appear")
	}
}

func TestResourceTask_Attributes(t *testing.T) {
	tid := uuid.New()
	owner := uuid.New()
	r := ResourceTask(tid, owner, true)
	if r.Type != EntityTask || r.ID != tid.String() {
		t.Errorf("type/id: %+v", r)
	}
	if r.Attributes["owner_id"] != owner.String() {
		t.Errorf("owner attr missing: %+v", r.Attributes)
	}
	if r.Attributes["is_public"] != true {
		t.Errorf("is_public attr: %+v", r.Attributes)
	}
}
