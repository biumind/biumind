package bridge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func newTestServer(t *testing.T, auth string) *httptest.Server {
	t.Helper()
	srv, err := NewServer(Options{
		AuthToken: auth,
		AgentFactory: func(_ AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				Model:               "test",
				APIKey:              "sk-fake",
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler())
}

func TestCreateSessionReturnsID(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got struct{ ID string }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Errorf("missing id in response")
	}
}

func TestAuthMiddlewareBlocks(t *testing.T) {
	ts := newTestServer(t, "secret")
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing token must 401, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/v1/code/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Errorf("valid token must 201, got %d", resp2.StatusCode)
	}
}

func TestDeleteRemovesSession(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var created struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/v1/code/sessions/"+created.ID, nil)
	dresp, _ := http.DefaultClient.Do(req)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Errorf("delete returned %d", dresp.StatusCode)
	}

	// Submit to a deleted session must 404.
	r2, _ := http.Post(
		ts.URL+"/v1/code/sessions/"+created.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hi"}`))
	r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("submit on deleted session = %d", r2.StatusCode)
	}
}

func TestSubmitRejectsEmptyPrompt(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()
	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	r2, err := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", bytes.NewReader([]byte(`{"prompt":""}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusBadRequest {
		t.Errorf("blank prompt should 400, got %d", r2.StatusCode)
	}
}

func TestParseLastEventIDQuery(t *testing.T) {
	r, _ := http.NewRequest("GET", "/x?last_event_id=42", nil)
	if got := parseLastEventID(r); got != 42 {
		t.Errorf("query parse failed: got %d", got)
	}
	r2, _ := http.NewRequest("GET", "/x", nil)
	if got := parseLastEventID(r2); got != 0 {
		t.Errorf("missing param should yield 0; got %d", got)
	}
	r3, _ := http.NewRequest("GET", "/x?last_event_id=garbage", nil)
	if got := parseLastEventID(r3); got != 0 {
		t.Errorf("non-numeric should yield 0; got %d", got)
	}
}

func TestCostEndpointReturnsSnapshot(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()
	resp, _ := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()

	r2, err := http.Get(ts.URL + "/v1/code/sessions/" + c.ID + "/cost")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("cost = %d", r2.StatusCode)
	}
	var got struct {
		Model string  `json:"Model"`
		USD   float64 `json:"USD"`
	}
	if err := json.NewDecoder(r2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Model == "" {
		t.Errorf("cost.Model missing")
	}
}

// set_permission_mode 控制分支：语义跟 REPL /mode 对齐（auto 直接
// SetMode；plan 走 EnterPlanMode/ExitPlanMode 簿记；未知模式回落
// default 不报错）。
func TestDispatchControlSetPermissionMode(t *testing.T) {
	ag, err := biumindkit.New(biumindkit.Options{
		Model:               "test",
		APIKey:              "sk-fake",
		LoadProjectMemory:   biumindkit.NoMemory,
		LoadProjectSettings: biumindkit.NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	rec := &sessionRec{id: "s1", agent: ag}

	dispatch := func(requestID, mode string) *sdkproto.SDKControlResponse {
		t.Helper()
		frame := rec.dispatchControl(&sdkproto.SDKControlRequest{
			Type:      sdkproto.TypeControlRequest,
			RequestID: requestID,
			Request:   &sdkproto.SetPermissionMode{Mode: mode},
		})
		resp, ok := frame.(*sdkproto.SDKControlResponse)
		if !ok {
			t.Fatalf("mode %q: unexpected frame type %T", mode, frame)
		}
		if resp.Response.Subtype != sdkproto.ControlSubtypeSuccess {
			t.Fatalf("mode %q: control error: %s", mode, resp.Response.Error)
		}
		return resp
	}

	// auto：直接 SetMode。
	dispatch("r1", "auto")
	if got := ag.Permissions().Mode(); got != permissions.ModeAuto {
		t.Errorf("after auto: mode=%v want auto", got)
	}

	// plan：EnterPlanMode 簿记 prePlanMode=auto。
	dispatch("r2", "plan")
	if got := ag.Permissions().Mode(); got != permissions.ModePlan {
		t.Errorf("after plan: mode=%v want plan", got)
	}
	if got := ag.Permissions().PrePlanMode(); got != permissions.ModeAuto {
		t.Errorf("prePlanMode=%v want auto", got)
	}

	// 离开 plan：ExitPlanMode 清簿记 + SetMode(default)。
	dispatch("r3", "default")
	if got := ag.Permissions().Mode(); got != permissions.ModeDefault {
		t.Errorf("after default: mode=%v want default", got)
	}
	if got := ag.Permissions().PrePlanMode(); got != "" {
		t.Errorf("prePlanMode should be cleared, got %q", got)
	}

	// 未知模式字符串 → 回落 default，不报错。
	dispatch("r4", "bogus")
	if got := ag.Permissions().Mode(); got != permissions.ModeDefault {
		t.Errorf("unknown mode: mode=%v want default", got)
	}

	// 成功应答带生效后的模式。
	resp := dispatch("r5", "auto")
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(resp.Response.Response, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mode != "auto" {
		t.Errorf("response mode=%q want auto", payload.Mode)
	}
}
