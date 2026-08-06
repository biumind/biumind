// validator.go — 用户上传 / 主动 test 时调上游一次, 判 Key 是否有效.
//
// MVP 支持:
//   anthropic / openai / deepseek / doubao / dashscope: GET /v1/models 或等价
//
// 其余 provider (volcengine / google / azure_openai / qwen / moonshot / baichuan)
// 走 PingResultUnknown — 视作 valid, 实际首次调用失败再 IncrementFailure 转 invalid.
//
// 网络超时硬性 5 秒 — 不让一次健康检查拖住整个请求.

package byok

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PingResult — 健康检查可能的状态.
type PingResult string

const (
	PingValid     PingResult = "valid"
	PingInvalid   PingResult = "invalid"   // 401 / 403 — 明确 key 无效
	PingNetwork   PingResult = "network"   // 网络 / 超时 — 不能判定; 保留旧 status
	PingUnknown   PingResult = "unknown"   // provider 不支持健康检查 — 视作 valid
)

// Validator 持有共享 *http.Client, 跨请求复用.
type Validator struct {
	httpClient *http.Client
}

func NewValidator() *Validator {
	return &Validator{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// SetHTTPClient — 测试时注入 httptest.NewServer 的 client.
func (v *Validator) SetHTTPClient(c *http.Client) { v.httpClient = c }

// PingArgs — 一次健康检查的全部输入.
//   Provider:   见 ValidProviders (00033 含 'custom')
//   APIKey:     明文 key
//   ConfigJSON: 遗留 (Azure/region); 与 BaseURL 并存时一般留空
//   BaseURL:    00033. custom 必填; 非 custom 时若非空则覆盖默认 endpoint
//   Protocol:   00033. custom 必填 (openai_compat/anthropic/google/...)
type PingArgs struct {
	Provider   string
	APIKey     string
	ConfigJSON []byte
	BaseURL    string
	Protocol   string
}

// Ping — 调上游确认 Key 有效性. custom 按 protocol 选 ping 形状 + 用 BaseURL
// 作 endpoint; 标准 provider 用默认 endpoint, BaseURL 非空时覆盖.
func (v *Validator) Ping(ctx context.Context, a PingArgs) PingResult {
	if a.APIKey == "" {
		return PingInvalid
	}
	if a.Provider == "custom" {
		return v.pingCustom(ctx, a)
	}
	switch a.Provider {
	case "anthropic":
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://api.anthropic.com/v1/models"),
			map[string]string{
				"x-api-key":         a.APIKey,
				"anthropic-version": "2023-06-01",
			})
	case "openai":
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://api.openai.com/v1/models"),
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	case "deepseek":
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://api.deepseek.com/v1/models"),
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	case "doubao":
		// 字节豆包 Ark API: 没有公开 /models, 但 /api/v3/models 列表是免费的
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://ark.cn-beijing.volces.com/api/v3/models"),
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	case "dashscope":
		// 阿里灵积: GET /api/v1/models 列表
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://dashscope.aliyuncs.com/api/v1/models"),
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	case "moonshot":
		return v.pingHTTP(ctx, "GET",
			orDefault(a.BaseURL, "https://api.moonshot.cn/v1/models"),
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	default:
		// volcengine / google / azure_openai / qwen / baichuan — MVP 不主动验;
		// 若传了 BaseURL, 按 OpenAI 兼容试测 (补 azure_openai 等 MVP 遗留).
		if ep := strings.TrimSpace(a.BaseURL); ep != "" {
			return v.pingHTTP(ctx, "GET", withV1(ep)+"/models",
				map[string]string{"Authorization": "Bearer " + a.APIKey})
		}
		return PingUnknown
	}
}

// pingCustom — custom provider: protocol 决定 ping 形状, BaseURL 作 endpoint.
// base_url 容错与 brain refresh.go / client direct_llm_probe 同款: 不含 /v1
// 自动补 (避免打到 web 首页返 HTML 误判).
func (v *Validator) pingCustom(ctx context.Context, a PingArgs) PingResult {
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if base == "" {
		return PingInvalid
	}
	switch a.Protocol {
	case "anthropic":
		return v.pingHTTP(ctx, "GET", base+"/v1/models", map[string]string{
			"x-api-key":         a.APIKey,
			"anthropic-version": "2023-06-01",
		})
	case "google":
		return v.pingHTTP(ctx, "GET",
			base+"/v1beta/models?key="+url.QueryEscape(a.APIKey), nil)
	default: // openai_compat + dashscope/volcengine chat 兼容形态
		return v.pingHTTP(ctx, "GET", withV1(base)+"/models",
			map[string]string{"Authorization": "Bearer " + a.APIKey})
	}
}

// withV1 — base 不含 /v1 时补 (OpenAI 兼容代理 /models 常在 /v1 下).
func withV1(base string) string {
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// orDefault — endpoint 空时用默认.
func orDefault(endpoint, def string) string {
	if ep := strings.TrimSpace(endpoint); ep != "" {
		return ep
	}
	return def
}

// pingHTTP — 通用 GET ping. 状态码语义:
//   2xx        → valid
//   401 / 403  → invalid (key 错 / 已撤销 / 配额耗尽 — 视作不可用)
//   其它       → network (5xx / 超时 / dns 错 — 不能判定, 保留旧 status)
func (v *Validator) pingHTTP(ctx context.Context, method, url string, headers map[string]string) PingResult {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return PingNetwork
	}
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		// dns / 拒连 / 超时 / context cancel — 一律 network (不改用户已有 status)
		return PingNetwork
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return PingValid
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		return PingInvalid
	default:
		return PingNetwork
	}
}
