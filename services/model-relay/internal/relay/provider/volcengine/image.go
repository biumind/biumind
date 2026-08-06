// image.go — VolcEngine Seedream 文生图 (同步 ImageAdaptor).
//
// Ark 图片生成是同步接口(与 dashscope 异步 task 不同):
//   POST {base}/images/generations
//     Body: {"model","prompt","size","image":[ref...],"watermark":false,
//            "response_format":"url","n":1}
//     Resp: {"data":[{"url":"...","size":"2048x2048"}], "usage":{...}}
//
// 不实现 AsyncImageAdaptor → images.go handler 走同步分支(单次 HTTP +
// ParseImageResponse)。
//
// 移植自 workers/aigc/biumind_aigc/providers/volcengine_image.py。

package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const imagePath = "/images/generations"

type arkImageRequest struct {
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	Size           string   `json:"size,omitempty"`
	Image          []string `json:"image,omitempty"` // 参考图 URL 列表
	Watermark      bool     `json:"watermark"`
	ResponseFormat string   `json:"response_format,omitempty"`
	N              int      `json:"n,omitempty"`
	Seed           int      `json:"seed,omitempty"`
}

// TranslateImageRequest 构造 Ark 同步图片生成请求。
func (a *Adaptor) TranslateImageRequest(
	ctx context.Context, req *provider.ImageRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("volcengine: missing API key")
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("volcengine: empty prompt")
	}

	prompt := req.Prompt
	// Ark 无独立 negative_prompt 字段,拼到 prompt 末尾(与 Python 同 best-effort)。
	if req.NegativePrompt != "" {
		prompt = fmt.Sprintf("%s\n负面词: %s", req.Prompt, req.NegativePrompt)
	}

	n := req.N
	if n <= 0 {
		n = 1
	}
	body := arkImageRequest{
		Model:          req.Model,
		Prompt:         prompt,
		Size:           resolveImageSize(req),
		Watermark:      false,
		ResponseFormat: "url",
		N:              n,
		Seed:           req.Seed,
	}
	if len(req.ReferenceImageURLs) > 0 {
		body.Image = req.ReferenceImageURLs
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("volcengine: marshal image: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+imagePath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// ParseImageResponse 解 Ark 同步响应 data:[{url}] → canonical ImageResponse。
func (a *Adaptor) ParseImageResponse(body []byte) (*provider.ImageResponse, error) {
	var ar struct {
		Data []struct {
			URL  string `json:"url"`
			Size string `json:"size,omitempty"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("volcengine: parse image response: %w", err)
	}
	if len(ar.Data) == 0 {
		if ar.Error.Code != "" || ar.Error.Message != "" {
			return nil, fmt.Errorf("volcengine image %s: %s", ar.Error.Code, ar.Error.Message)
		}
		return nil, fmt.Errorf("volcengine: image response has no data")
	}
	resp := &provider.ImageResponse{}
	for _, d := range ar.Data {
		if d.URL == "" {
			continue
		}
		resp.Data = append(resp.Data, provider.ImageData{URL: d.URL})
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("volcengine: image response data has no url")
	}
	return resp, nil
}

// resolveImageSize — 优先用显式 Size;否则按 aspect_ratio + resolution 查
// Ark 的 2K/3K 尺寸表(与 volcengine_image.py 对齐)。
func resolveImageSize(req *provider.ImageRequest) string {
	if s := strings.TrimSpace(req.Size); s != "" {
		return s
	}
	if req.AspectRatio == "" {
		return ""
	}
	table := ark2KSizes
	if strings.EqualFold(req.Resolution, "3K") {
		table = ark3KSizes
	}
	return table[req.AspectRatio]
}

// Ark Seedream 尺寸表(移植自 volcengine_image.py get2KSize/get3KSize)。
var ark2KSizes = map[string]string{
	"1:1":  "2048x2048",
	"4:3":  "2304x1728",
	"3:4":  "1728x2304",
	"16:9": "2848x1600",
	"9:16": "1600x2848",
	"3:2":  "2496x1664",
	"2:3":  "1664x2496",
	"21:9": "3136x1344",
}

var ark3KSizes = map[string]string{
	"1:1":  "3072x3072",
	"4:3":  "3456x2592",
	"3:4":  "2592x3456",
	"16:9": "4096x2304",
	"9:16": "2304x4096",
	"2:3":  "2496x3744",
	"3:2":  "3744x2496",
	"21:9": "4704x2016",
}

// 编译期断言 — 同步 ImageAdaptor(不实现 AsyncImageAdaptor)。
var _ provider.ImageAdaptor = (*Adaptor)(nil)
