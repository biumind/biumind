package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/google/uuid"
)

// fakeModelLister implements modelLister for unit tests, no DB required.
type fakeModelLister struct {
	gotFilter registry.ModelFilter
	models    []registry.Model
	err       error
}

func (f *fakeModelLister) List(_ context.Context, filter registry.ModelFilter) ([]registry.Model, error) {
	f.gotFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func TestPublicModels_DefaultFiltersToActive(t *testing.T) {
	f := &fakeModelLister{}
	h := &PublicModelsHandler{Models: f}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if f.gotFilter.Status != registry.StatusActive {
		t.Errorf("default should pin Status=active; got %q", f.gotFilter.Status)
	}
	if f.gotFilter.Limit != publicModelsMaxLimit {
		t.Errorf("Limit=%d want %d", f.gotFilter.Limit, publicModelsMaxLimit)
	}
}

func TestPublicModels_AllStatusBypassesFilter(t *testing.T) {
	f := &fakeModelLister{}
	h := &PublicModelsHandler{Models: f}
	req := httptest.NewRequest(http.MethodGet, "/v1/models?status=all", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if f.gotFilter.Status != "" {
		t.Errorf("status=all should leave filter Status empty; got %q", f.gotFilter.Status)
	}
}

// 字段级测试: 确认返回的 JSON 只含公开字段, 不泄露 admin 内部字段。
// P6: min_plan/max_output 现属公开字段 (picker 用); mode 同。
func TestPublicModels_ResponseHasOnlyPublicFields(t *testing.T) {
	f := &fakeModelLister{
		models: []registry.Model{
			{
				Code:          "claude-sonnet-4-6",
				DisplayName:   "Claude Sonnet 4.6",
				Family:        "claude",
				ContextWindow: 200000,
				MaxOutput:     8192,
				Mode:          "chat",
				Capabilities: registry.Capabilities{
					Vision: true, Tools: true,
				},
				// 这些字段绝不应该出现在 response 里:
				Status:    registry.StatusActive,
				MinPlan:   registry.Plan("pro"),
				SortOrder: 99,
			},
		},
	}
	h := &PublicModelsHandler{Models: f}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// 必须有的公开字段
	for _, want := range []string{
		`"code":"claude-sonnet-4-6"`,
		`"display_name":"Claude Sonnet 4.6"`,
		`"family":"claude"`,
		`"context_window":200000`,
		`"max_output":8192`,
		`"mode":"chat"`,
		`"capabilities"`,
		`"min_plan":"pro"`, // pro 显 (free 省略)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q\nbody=%s", want, body)
		}
	}

	// 绝不能出现的 admin 内部字段
	for _, leak := range []string{
		`"status"`, `"sort_order"`, `"upstream_ref"`,
		`"manual_override"`, `"routing_strategy"`, `"id"`,
		`"markup_ratio"`, `"min_charge"`, `"max_charge_per_request"`, // pricing 内部
	} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks admin field %q\nbody=%s", leak, body)
		}
	}
}

// 仅 GET 通过, 其他方法 405。
func TestPublicModels_RejectsNonGet(t *testing.T) {
	h := &PublicModelsHandler{Models: &fakeModelLister{}}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/models", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s want 405; got %d", method, w.Code)
		}
	}
}

// Models 依赖 nil → 503 (而不是 panic)。
func TestPublicModels_NilDependency(t *testing.T) {
	h := &PublicModelsHandler{Models: nil}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil deps want 503; got %d", w.Code)
	}
}

// List 返错 → 500 + 错误 code, 不 panic。
func TestPublicModels_RepoErrorTo500(t *testing.T) {
	h := &PublicModelsHandler{Models: &fakeModelLister{err: errors.New("db down")}}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("repo err want 500; got %d", w.Code)
	}
}

// 空列表也应该返 200 + items=[],别返 null 让客户端 cast 炸。
func TestPublicModels_EmptyListReturnsEmptyArray(t *testing.T) {
	h := &PublicModelsHandler{Models: &fakeModelLister{models: nil}}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("empty list want 200; got %d", w.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items == nil {
		t.Errorf("items=null; want []")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items len=%d; want 0", len(resp.Items))
	}
}

// fakeModelPricer 实现 modelPricer 切面 (单测, 无 DB)。
type fakeModelPricer struct {
	prices map[uuid.UUID]registry.Pricing
	err    error
}

func (f *fakeModelPricer) BatchLatest(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]registry.Pricing, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.prices, nil
}

// P6: pricing 返 markup 后实际计费价。MarkupRatio=0 → 默认 3.0。
// 成本 input=5, output=20 → 标价 input=15, output=60。
func TestPublicModels_PricingIsMarkupApplied(t *testing.T) {
	mID := uuid.New()
	f := &fakeModelLister{models: []registry.Model{{ID: mID, Code: "m1", Status: registry.StatusActive}}}
	pricer := &fakeModelPricer{prices: map[uuid.UUID]registry.Pricing{mID: {
		Currency: registry.CurrencyUSD, InputPerMTok: 5, OutputPerMTok: 20, MarkupRatio: 0,
	}}}
	h := &PublicModelsHandler{Models: f, Pricer: pricer}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 默认 markup 3.0: 5*3=15, 20*3=60
	for _, want := range []string{
		`"currency":"USD"`,
		`"input_per_mtok":15`,
		`"output_per_mtok":60`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\nbody=%s", want, body)
		}
	}
	// markup_ratio 本身绝不暴露
	if strings.Contains(body, `"markup_ratio"`) {
		t.Errorf("markup_ratio leaked\nbody=%s", body)
	}
}

// 显式 MarkupRatio=2 → 标价 = 成本 × 2 (非默认 3)。
func TestPublicModels_PricingExplicitMarkup(t *testing.T) {
	mID := uuid.New()
	f := &fakeModelLister{models: []registry.Model{{ID: mID, Code: "m1"}}}
	pricer := &fakeModelPricer{prices: map[uuid.UUID]registry.Pricing{mID: {
		Currency: registry.CurrencyUSD, InputPerMTok: 10, MarkupRatio: 2,
	}}}
	h := &PublicModelsHandler{Models: f, Pricer: pricer}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), `"input_per_mtok":20`) { // 10*2
		t.Errorf("explicit markup 2 not applied\nbody=%s", w.Body.String())
	}
}

// Pricer nil → DTO 不含 pricing 字段 (picker 仍可用, 仅无价 chip)。
func TestPublicModels_NilPricerOmitsPricing(t *testing.T) {
	f := &fakeModelLister{models: []registry.Model{{Code: "m1"}}}
	h := &PublicModelsHandler{Models: f, Pricer: nil}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if strings.Contains(w.Body.String(), `"pricing"`) {
		t.Errorf("nil pricer should omit pricing\nbody=%s", w.Body.String())
	}
}

// Pricer 报错 → 不阻断, DTO pricing 缺失 (warn log)。
func TestPublicModels_PricerErrorOmitsPricing(t *testing.T) {
	f := &fakeModelLister{models: []registry.Model{{Code: "m1"}}}
	pricer := &fakeModelPricer{err: errors.New("db down")}
	h := &PublicModelsHandler{Models: f, Pricer: pricer}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("pricer err should not fail request; got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `"pricing"`) {
		t.Errorf("pricer err should omit pricing\nbody=%s", w.Body.String())
	}
}

// free plan 省略 min_plan (避免噪音); pro/team 显。
func TestPublicModels_FreePlanOmitsMinPlan(t *testing.T) {
	f := &fakeModelLister{models: []registry.Model{{Code: "m1", MinPlan: registry.PlanFree}}}
	h := &PublicModelsHandler{Models: f}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), `"min_plan"`) {
		t.Errorf("free plan should omit min_plan\nbody=%s", w.Body.String())
	}
}
