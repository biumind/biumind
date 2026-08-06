// adaptor.go — VolcEngine (火山豆包 Ark) 多模态 adaptor.
//
// 段 3.6: model-relay 接管 AIGC dispatch 后, 把 workers/aigc 的
// volcengine_image.py(同步 Seedream)+ volcengine_video.py(异步 Seedance)
// 移植成 Go adaptor, worker 退化为调 model-relay /v1/internal/generations。
//
// Modality:
//   image_generation — 同步 (ImageAdaptor):POST /images/generations 一次返
//                      data:[{url}](OpenAI 兼容形态)。
//   video_generation — 异步 (VideoAdaptor + AsyncVideoAdaptor):
//                      POST /contents/generations/tasks → {id};
//                      GET  /contents/generations/tasks/{id} → {status, content}。
//
// 鉴权:Authorization: Bearer ${API_KEY}(Ark API key,与 dashscope 同 Bearer 形态)。
// 默认地域北京 ark.cn-beijing.volces.com;creds.BaseURL 可覆盖。

package volcengine

const (
	defaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
)

// Adaptor 实现 VolcEngine 的 image(同步)+ video(异步)生成。
// 不实现 chat(Doubao chat 走 openai_compat → provider/openai)。
type Adaptor struct{}

func New() *Adaptor { return &Adaptor{} }

func (a *Adaptor) Name() string { return "volcengine" }

// Capabilities — 当前支持 Seedream 文生图 + Seedance 文生视频。
func (a *Adaptor) Capabilities() []string {
	return []string{"image_generation", "video_generation"}
}
