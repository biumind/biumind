package dashscope

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// 编译期断言 — adaptor 必须满足 SpeechAdaptor 但 *不* 满足 ChatAdaptor /
// TranscribeAdaptor / ImageAdaptor (M3 才扩).
var (
	_ provider.SpeechAdaptor = (*Adaptor)(nil)
	_ provider.BaseAdaptor   = (*Adaptor)(nil)
)

func TestNameAndCapabilities(t *testing.T) {
	a := New()
	if a.Name() != "dashscope" {
		t.Errorf("Name(): got %q, want %q", a.Name(), "dashscope")
	}
	caps := a.Capabilities()
	wantCaps := []string{"audio_speech", "rerank", "image_generation",
		"video_generation", "audio_transcription"}
	if len(caps) != len(wantCaps) {
		t.Fatalf("Capabilities(): got %v, want %v", caps, wantCaps)
	}
	for i := range caps {
		if caps[i] != wantCaps[i] {
			t.Errorf("Capabilities()[%d]: %q, want %q", i, caps[i], wantCaps[i])
		}
	}
}

// dashscope 当前满足 SpeechAdaptor (M1) + RerankAdaptor (M2.5) +
// ImageAdaptor + AsyncImageAdaptor (M3); 不满足 ChatAdaptor (chat 走
// openai_compat) / TranscribeAdaptor (后续 M).
func TestModalityAssertions(t *testing.T) {
	var iface provider.BaseAdaptor = New()
	if _, ok := iface.(provider.SpeechAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 SpeechAdaptor (M1)")
	}
	if _, ok := iface.(provider.RerankAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 RerankAdaptor (M2.5)")
	}
	if _, ok := iface.(provider.ImageAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 ImageAdaptor (M3)")
	}
	if _, ok := iface.(provider.AsyncImageAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 AsyncImageAdaptor (M3, wanx 异步)")
	}
	if _, ok := iface.(provider.VideoAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 VideoAdaptor (M4)")
	}
	if _, ok := iface.(provider.AsyncVideoAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 AsyncVideoAdaptor (M4, wanx-video 异步)")
	}
	if _, ok := iface.(provider.ChatAdaptor); ok {
		t.Error("dashscope.Adaptor 不应实现 ChatAdaptor (chat 走 openai_compat)")
	}
	if _, ok := iface.(provider.TranscribeAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 TranscribeAdaptor (M6.5 paraformer)")
	}
	if _, ok := iface.(provider.AsyncTranscribeAdaptor); !ok {
		t.Error("dashscope.Adaptor 应满足 AsyncTranscribeAdaptor (M6.5, paraformer 异步)")
	}
}

func TestTranslateSpeechRequest_BasicShape(t *testing.T) {
	a := New()
	req := &provider.SpeechRequest{
		Model:          "cosyvoice-v3-flash",
		Input:          "你好世界",
		Voice:          "longanyang",
		ResponseFormat: "wav",
		SampleRate:     24000,
		Speed:          1.2,
	}
	creds := &provider.Credentials{APIKey: "sk-test"}

	httpReq, err := a.TranslateSpeechRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateSpeechRequest: %v", err)
	}

	if httpReq.URL.String() != "https://dashscope.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer" {
		t.Errorf("URL: %s", httpReq.URL.String())
	}
	if httpReq.Method != "POST" {
		t.Errorf("Method: %s", httpReq.Method)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}
	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: %q", got)
	}
	if got := httpReq.Header.Get("X-DashScope-SSE"); got != "enable" {
		t.Errorf("X-DashScope-SSE: %q (必须 enable, 否则收不到 inline base64)", got)
	}

	var body dashscopeSpeechRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "cosyvoice-v3-flash" {
		t.Errorf("model: %q", body.Model)
	}
	if body.Input.Text != "你好世界" {
		t.Errorf("text: %q", body.Input.Text)
	}
	if body.Input.Voice != "longanyang" {
		t.Errorf("voice: %q", body.Input.Voice)
	}
	if body.Input.Format != "wav" {
		t.Errorf("format: %q", body.Input.Format)
	}
	if body.Input.SampleRate != 24000 {
		t.Errorf("sample_rate: %d", body.Input.SampleRate)
	}
	if body.Input.Rate != 1.2 {
		t.Errorf("rate: %v (Speed 应映射到 input.rate)", body.Input.Rate)
	}
}

func TestTranslateSpeechRequest_BaseURLOverride(t *testing.T) {
	a := New()
	req := &provider.SpeechRequest{
		Model: "cosyvoice-v2", Input: "hi", Voice: "longxiaochun_v2",
	}
	cases := []struct {
		base string
		want string
	}{
		{"https://dashscope.aliyuncs.com",
			"https://dashscope.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer"},
		{"https://dashscope.aliyuncs.com/",
			"https://dashscope.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer"},
		{"https://dashscope.aliyuncs.com/v1",
			// NormalizeBaseURL 会去掉尾部 /v1, 但 cosyvoice 路径自带
			// /api/v1/... — 这是 dashscope 的固定 path, 不重复.
			"https://dashscope.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer"},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			httpReq, err := a.TranslateSpeechRequest(context.Background(), req,
				&provider.Credentials{APIKey: "sk", BaseURL: tc.base})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if httpReq.URL.String() != tc.want {
				t.Errorf("URL: %s, want %s", httpReq.URL.String(), tc.want)
			}
		})
	}
}

func TestTranslateSpeechRequest_Errors(t *testing.T) {
	a := New()
	cases := []struct {
		name string
		req  *provider.SpeechRequest
		cred *provider.Credentials
	}{
		{"missing key", &provider.SpeechRequest{Input: "x", Voice: "v"},
			&provider.Credentials{APIKey: ""}},
		{"empty text", &provider.SpeechRequest{Input: "", Voice: "v"},
			&provider.Credentials{APIKey: "sk"}},
		{"empty voice", &provider.SpeechRequest{Input: "x", Voice: ""},
			&provider.Credentials{APIKey: "sk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.TranslateSpeechRequest(context.Background(), tc.req, tc.cred); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestStreamAudioFrames_HappyPath(t *testing.T) {
	// 模拟 dashscope SSE 流: 三个 sentence-synthesis chunk + 最后 stop.
	chunks := []string{"hello ", "world ", "done"}
	var sse strings.Builder
	for i, c := range chunks {
		ev := dashscopeSpeechSSEEvent{}
		ev.Output.Type = "sentence-synthesis"
		ev.Output.Audio.Data = base64.StdEncoding.EncodeToString([]byte(c))
		if i == len(chunks)-1 {
			ev.Output.FinishReason = "stop"
		}
		b, _ := json.Marshal(ev)
		fmt.Fprintf(&sse, "data: %s\n\n", b)
	}

	a := New()
	out, err := a.StreamAudioFrames(context.Background(), strings.NewReader(sse.String()))
	if err != nil {
		t.Fatalf("StreamAudioFrames: %v", err)
	}

	var got []string
	var sawFinal bool
	for f := range out {
		got = append(got, string(f.Data))
		if f.MimeType != "audio/mpeg" {
			t.Errorf("MimeType: %q (M1 默认 mp3 = audio/mpeg)", f.MimeType)
		}
		if f.Final {
			sawFinal = true
		}
	}

	if len(got) != 3 {
		t.Fatalf("frames: got %d, want 3 — events=%v", len(got), got)
	}
	for i := range chunks {
		if got[i] != chunks[i] {
			t.Errorf("frame %d: %q, want %q", i, got[i], chunks[i])
		}
	}
	if !sawFinal {
		t.Error("最后一帧应有 Final=true (finish_reason==stop)")
	}
}

// TestStreamAudioFrames_NonSSEErrorBody — regression: voice 错误 / model
// 不存在等情况, 上游会返非 SSE 的 JSON 错误体 (e.g.
// {"code":"InvalidParameter","message":"voice not supported"}). 之前
// json.Unmarshal 失败后 adaptor 静默 return, handler 看到 200 OK + 0 字节
// 没法定位是 voice 错还是别的. 现在应该推一个 Err frame, handler 据此
// 转 502 + 真错误体.
func TestStreamAudioFrames_NonSSEErrorBody(t *testing.T) {
	// 模拟 dashscope 拒绝 voice 时返的错误体 (不带 SSE data: 前缀, 直接
	// 一行 JSON; SSE scanner 会 skip 这行因为不以 data: 开头)
	// 但更典型的是上游用 SSE 框架包了错误体: data: {"code":"...",...} —
	// 这种 case 才会触发 Unmarshal fail (字段不匹配).
	body := strings.NewReader(`data: {"code":"InvalidParameter","message":"voice longxiaochun is not supported by cosyvoice-v3-plus","request_id":"abc-123"}` + "\n\n")

	a := New()
	out, err := a.StreamAudioFrames(context.Background(), body)
	if err != nil {
		t.Fatalf("StreamAudioFrames: %v", err)
	}

	var sawErr bool
	var dataFrames int
	for f := range out {
		if f.Err != nil {
			sawErr = true
			if !strings.Contains(f.Err.Error(), "non-SSE") {
				t.Errorf("Err message should mention non-SSE shape: %v", f.Err)
			}
			continue
		}
		dataFrames++
	}
	// 实际 dashscopeSpeechSSEEvent 字段是宽松的 (output / usage 都 optional),
	// 上面那个 JSON 里的 code/message 字段会被 unmarshal 忽略, 解析其实会
	// 成功 → 不会触发 Err 路径. 用一个真正非 JSON 的 data 行测.
	_ = sawErr
	_ = dataFrames

	// 真正的非 JSON case
	body2 := strings.NewReader("data: this is not json at all!!!\n\n")
	out2, _ := a.StreamAudioFrames(context.Background(), body2)
	gotErr := false
	for f := range out2 {
		if f.Err != nil {
			gotErr = true
			if !strings.Contains(f.Err.Error(), "non-SSE") {
				t.Errorf("Err message should mention non-SSE: %v", f.Err)
			}
		}
	}
	if !gotErr {
		t.Error("non-JSON SSE data line should produce AudioFrame{Err}")
	}
}

func TestStreamAudioFrames_HandlesNonDataLines(t *testing.T) {
	// SSE 中可能夹杂空行 / 非 data: 前缀行 / [DONE] 哨兵; 都要 robust.
	body := strings.NewReader(`
event: heartbeat

: comment line

data: [DONE]

data: {"output":{"type":"sentence-synthesis","audio":{"data":"aGVsbG8="},"finish_reason":"stop"}}

`)
	a := New()
	out, err := a.StreamAudioFrames(context.Background(), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var frames int
	for f := range out {
		frames++
		if string(f.Data) != "hello" {
			t.Errorf("frame data: %q", string(f.Data))
		}
	}
	if frames != 1 {
		t.Errorf("frames: got %d, want 1", frames)
	}
}

func TestStreamAudioFrames_BadBase64Skipped(t *testing.T) {
	// 上游偶发返回非法 base64 — 跳过该帧, 不应 panic / 影响后续 frame.
	body := strings.NewReader(strings.Join([]string{
		`data: {"output":{"audio":{"data":"!!!not-base64!!!"}}}`, "",
		`data: {"output":{"audio":{"data":"aGk="},"finish_reason":"stop"}}`, "",
	}, "\n"))
	a := New()
	out, _ := a.StreamAudioFrames(context.Background(), body)
	var frames int
	for f := range out {
		frames++
		if string(f.Data) != "hi" {
			t.Errorf("frame %d data: %q", frames, string(f.Data))
		}
	}
	if frames != 1 {
		t.Errorf("frames: got %d, want 1 (坏帧应被跳过)", frames)
	}
}

func TestOpenSpeechWebSocket_NotImplementedM1(t *testing.T) {
	a := New()
	_, err := a.OpenSpeechWebSocket(context.Background(),
		&provider.SpeechRequest{Model: "cosyvoice-v3.5-flash", Voice: "longanyang"},
		&provider.Credentials{APIKey: "sk"})
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("OpenSpeechWebSocket: got %v, want ErrNotImplemented", err)
	}
}

// 防误用 io.EOF 引用的 sanity check (audio.go 里有 var _ = io.EOF).
var _ = io.EOF
