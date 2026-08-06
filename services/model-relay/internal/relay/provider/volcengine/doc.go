// Package volcengine is the relay adaptor namespace for VolcEngine (火山豆包 Ark).
//
// Modality files (段 3.6: model-relay 接管 AIGC egress):
//
//	image.go   VolcEngine Seedream 文生图 — 同步 (ImageAdaptor)
//	video.go   VolcEngine Seedance 文生视频 — 异步 (AsyncVideoAdaptor)
//
// 移植自 workers/aigc/biumind_aigc/providers/volcengine_image.py +
// volcengine_video.py(Phase 3 删除 Python 直连 provider)。
//
// chat path: not handled here (VolcEngine chat — Doubao — is OpenAI-compat
// and routes through provider/openai when admin configures it).
package volcengine
