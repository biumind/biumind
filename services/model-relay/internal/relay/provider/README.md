# relay/provider/ 组织原则

每个上游供应商一个子目录,子目录内**按 modality 拆文件**:

```
provider/
├── anthropic/
│   └── chat.go              # POST /v1/messages 协议适配 (Anthropic-native)
├── openai/
│   └── chat.go              # OpenAI-compat (覆盖 OpenAI / DeepSeek / Doubao / Azure)
├── dashscope/                # 段 3+ 填充
│   ├── image.go              # 通义万相 T2I
│   └── video.go              # 通义万相 T2V
├── volcengine/               # 段 3+ 填充
│   ├── image.go              # Seedream
│   └── video.go              # Seedance
└── kling/                    # video TTS, P4 之后扩
```

## 选 vendor-first vs modality-first?

LiteLLM 用 vendor-first,
我们也用 vendor-first。`BiuMind-Model-Config-Design.md §3.4.4` 写的是
modality-first 但同一目录强制 Go package 共享会让 vendor 之间符号冲突
(`anthropic.Adaptor` vs `openai.Adaptor` 必须改名)。vendor-first 的优势:

- 每个 vendor 一个独立 Go package, 符号独立 (`anthropic.Adaptor` 不变)
- 加新 modality 只在 vendor 子目录加 1 个文件, 不动 import path
- 同一个 OpenAI key 的 chat / embeddings / image 方便共享 client / token
- 与 LiteLLM 的目录结构 1:1 对齐, 后续 import 它们的元数据更顺

## Adaptor 接口分裂 (P4 段 3+)

`provider.go` 当前 `Adaptor` 接口是 chat 专用 (Request 含 Messages / Tools).
段 3+ 新加 `ImageAdaptor` / `VideoAdaptor` 接口, 各 modality 文件实现各自接口.
Resolver 按 `model.mode` 拿对应接口的实例.

## 当前 (段 3.5 后) 状态

- ✅ chat: anthropic / openai (覆盖 OpenAI 兼容大部分 vendor)
- ⏳ image / video / digital_human / hotparse: 由 services/aigc orchestrator
  + workers/aigc/ Python providers 处理, **不在 relay/ 里**. 段 3.6+ 才
  把这部分能力下沉到 model-relay 直接 dispatch.
