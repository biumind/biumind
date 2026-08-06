package volcengine

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// 编译期 + 运行期断言:image 同步、video 异步。
var (
	_ provider.ImageAdaptor      = (*Adaptor)(nil)
	_ provider.VideoAdaptor      = (*Adaptor)(nil)
	_ provider.AsyncVideoAdaptor = (*Adaptor)(nil)
)

func TestNameCapabilities(t *testing.T) {
	a := New()
	if a.Name() != "volcengine" {
		t.Errorf("Name: %q", a.Name())
	}
	caps := a.Capabilities()
	if len(caps) != 2 || caps[0] != "image_generation" || caps[1] != "video_generation" {
		t.Errorf("Capabilities: %v", caps)
	}
}

func TestModalityAssertions(t *testing.T) {
	var iface provider.BaseAdaptor = New()
	if _, ok := iface.(provider.ImageAdaptor); !ok {
		t.Error("应满足 ImageAdaptor")
	}
	if _, ok := iface.(provider.AsyncVideoAdaptor); !ok {
		t.Error("应满足 AsyncVideoAdaptor")
	}
	// image 是同步 — 不应实现 AsyncImageAdaptor。
	if _, ok := iface.(provider.AsyncImageAdaptor); ok {
		t.Error("volcengine image 是同步, 不应实现 AsyncImageAdaptor")
	}
	// 不实现 chat。
	if _, ok := iface.(provider.ChatAdaptor); ok {
		t.Error("不应实现 ChatAdaptor")
	}
}

func TestTranslateImageRequest(t *testing.T) {
	a := New()
	req := &provider.ImageRequest{
		Model: "doubao-seedream-4-0", Prompt: "柯基",
		NegativePrompt: "模糊", AspectRatio: "16:9", Resolution: "2K", N: 1,
		ReferenceImageURLs: []string{"https://ref/a.png"},
	}
	httpReq, err := a.TranslateImageRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if httpReq.URL.String() != defaultBaseURL+imagePath {
		t.Errorf("URL: %s", httpReq.URL.String())
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-x" {
		t.Errorf("auth: %q", got)
	}
	var body arkImageRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)
	if body.Size != "2848x1600" {
		t.Errorf("size aspect→table: %q, want 2848x1600", body.Size)
	}
	if body.Watermark {
		t.Error("watermark 应为 false")
	}
	if len(body.Image) != 1 || body.Image[0] != "https://ref/a.png" {
		t.Errorf("image refs: %v", body.Image)
	}
	if body.Prompt == "柯基" {
		t.Error("negative_prompt 应拼进 prompt")
	}
}

func TestTranslateImageRequest_ExplicitSizeWins(t *testing.T) {
	a := New()
	req := &provider.ImageRequest{Model: "m", Prompt: "p", Size: "1024x1024", AspectRatio: "16:9"}
	httpReq, _ := a.TranslateImageRequest(context.Background(), req, &provider.Credentials{APIKey: "k"})
	var body arkImageRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)
	if body.Size != "1024x1024" {
		t.Errorf("显式 size 应优先: %q", body.Size)
	}
}

func TestParseImageResponse(t *testing.T) {
	a := New()
	resp, err := a.ParseImageResponse([]byte(`{"data":[{"url":"https://oss/x.png","size":"2048x2048"}]}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].URL != "https://oss/x.png" {
		t.Errorf("data: %+v", resp.Data)
	}
}

func TestParseImageResponse_Error(t *testing.T) {
	a := New()
	if _, err := a.ParseImageResponse([]byte(`{"data":[],"error":{"code":"X","message":"bad"}}`)); err == nil {
		t.Error("空 data + error 应返错")
	}
}

func TestVideoSubmitAndPoll(t *testing.T) {
	a := New()
	// submit body
	httpReq, err := a.TranslateVideoRequest(context.Background(), &provider.VideoRequest{
		Model: "doubao-seedance", Prompt: "海浪", AspectRatio: "16:9",
		Resolution: "720P", DurationSeconds: 5,
		FirstFrameURL: "https://f/1.png", LastFrameURL: "https://f/2.png",
	}, &provider.Credentials{APIKey: "k"})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if httpReq.URL.String() != defaultBaseURL+videoTasksPath {
		t.Errorf("URL: %s", httpReq.URL.String())
	}
	raw, _ := io.ReadAll(httpReq.Body)
	var vb arkVideoRequest
	_ = json.Unmarshal(raw, &vb)
	if vb.Resolution != "720p" {
		t.Errorf("resolution 应小写: %q", vb.Resolution)
	}
	if vb.Duration != 5 || vb.Ratio != "16:9" {
		t.Errorf("duration/ratio: %d %q", vb.Duration, vb.Ratio)
	}
	// content: text + first_frame(role) + last_frame(role)
	if len(vb.Content) != 3 || vb.Content[1].Role != "first_frame" || vb.Content[2].Role != "last_frame" {
		t.Errorf("content roles: %+v", vb.Content)
	}

	// submit parse
	id, err := a.ParseVideoSubmit([]byte(`{"id":"task-123"}`))
	if err != nil || id != "task-123" {
		t.Fatalf("submit parse: id=%q err=%v", id, err)
	}

	// poll: running → succeeded
	st, _, _ := a.ParseVideoPollResponse([]byte(`{"status":"running"}`))
	if st != "running" {
		t.Errorf("running: %q", st)
	}
	st, res, err := a.ParseVideoPollResponse([]byte(`{"status":"succeeded","content":{"video_url":"https://v/x.mp4","last_frame_url":"https://v/c.png"}}`))
	if err != nil || st != "succeeded" {
		t.Fatalf("succeeded: st=%q err=%v", st, err)
	}
	if res.Data[0].URL != "https://v/x.mp4" || res.Data[0].CoverImageURL != "https://v/c.png" {
		t.Errorf("video data: %+v", res.Data[0])
	}
	// failed
	st, _, err = a.ParseVideoPollResponse([]byte(`{"status":"failed","error":{"code":"E","message":"oops"}}`))
	if st != "failed" || err == nil {
		t.Errorf("failed: st=%q err=%v", st, err)
	}
}
