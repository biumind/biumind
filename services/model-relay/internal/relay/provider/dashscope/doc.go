// Package dashscope implements provider.Adaptor for 阿里云 DashScope
// 私有协议. v0.3 多模态网关用它来托管 cosyvoice / paraformer / wanx /
// qwen-image / wanx-video 等 AIGC 模型 (非 OpenAI-compat 形态).
//
// 现状 (v0.3 M1):
//   - audio.go    SpeechAdaptor (cosyvoice TTS, HTTP + SSE streaming)
//     TranscribeAdaptor stub (paraformer 走异步 task, M3 实现)
//
// 计划增量:
//   - image.go    ImageAdaptor / TaskAdaptor (wanx / qwen-image, M3)
//   - video.go    TaskAdaptor (wanx-video, M3)
//   - chat.go     ChatAdaptor 走 dashscope 原生 text-generation (待排, 默认
//     dashscope 上的 chat 模型仍用 protocol=openai_compat 走
//     provider/openai 包)
//
// 协议路由约定: Provider 行 protocol=dashscope → 路由到 Name="dashscope".
// dashscope 上 chat 模型可继续用 protocol=openai_compat 走 openai adaptor,
// 两种 protocol 在同一物理上游下并存. 见 docs/BiuMind-Multimodal-Gateway-Design.md §3.
package dashscope
