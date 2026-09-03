# biumind-wiki-parse-worker

BiuMind Wiki 源文件解析 worker。订阅 upload 入库事件 + tick 兜底，从 MinIO
拉文件 → 提取纯文本 → 回写 `wiki_sources.extracted_text / content_hash /
parse_status` → 闭环 upload ingest + source overlap 信号 + 项目内 content_hash
去重检测。

## 架构

- **触发**：NATS `biumind.<env>.brain.wiki.parse.requested`（主路径）+ 60s tick
  rescan `parse-queue`（兜底：NATS 漏发 / worker 宕机积压 / dev NoopBus）
- **文件下载**：brain `GET /v1/internal/wiki/sources/{id}/blob-presign` 签发
  presigned URL，worker httpx 流式下载（**不碰 MinIO 凭据**）
- **回写**：brain `POST /v1/internal/wiki/sources/{id}/parse-result`，done 时
  brain 同步做项目内 source dedup → `review_items`
- **解析**：pypdf（PDF 文本层）+ mammoth（DOCX）+ openpyxl（XLSX）+
  python-pptx（PPTX）+ ebooklib（EPUB，章节 HTML 复用 tag-strip）+
  utf-8（MD/TXT/code/JSON）+ stdlib tag-strip（HTML）
- **PDF OCR（可选，B1）**：`BIUMIND_WIKI_PARSE_OCR_ENABLED=true` 后**全量 PDF**
  先走自部署 MinerU（mineru-api `/tasks` 协议：multipart 提交 → 3s 轮询 →
  取 `md_content`），成功回写 `parser='mineru'`；可重试失败（网络/5xx/轮询
  超时）降级 pypdf 文本层回写 `parser='pypdf'`；终态失败（4xx/任务失败/
  md_content 空）`parse_error` 带 `[terminal]` 前缀，brain 不再重扫
- **并发**：`JobDispatcher` = `asyncio.create_task` +
  `Semaphore(BIUMIND_WIKI_PARSE_MAX_CONCURRENCY)` —— OCR 单任务分钟级，
  NATS handler 与 tick loop 顺序 await 会堵整队

## Env

| Env | 默认 | 说明 |
|---|---|---|
| `BIUMIND_NATS_URL` | `nats://localhost:4222` | NATS 连接（fallback `NATS_URL`） |
| `BIUMIND_ENV` | `dev` | 环境前缀（拼 subject） |
| `BIUMIND_BRAIN_URL` | — | brain base URL（**必填**） |
| `BIUMIND_INTERNAL_TOKEN` | — | 与 brain 共享 service token（**必填**） |
| `BIUMIND_WIKI_PARSE_QUEUE` | `brain-wiki-parse` | NATS queue group |
| `BIUMIND_WIKI_PARSE_INTERVAL_S` | `60` | rescan tick 间隔秒 |
| `BIUMIND_WIKI_PARSE_MAX_BYTES` | `209715200` | 单文件上限（200MB，zip-bomb 防护） |
| `BIUMIND_WIKI_PARSE_OCR_ENABLED` | `false` | PDF OCR 开关（自部署 MinerU；启用后全量 PDF 走 MinerU，可重试失败降级 pypdf） |
| `BIUMIND_MINERU_API_BASE` | `http://mineru:8000` | mineru-api base URL（内网服务间调用，不经 nginx 不暴露公网） |
| `BIUMIND_OCR_POLL_TIMEOUT_S` | `900` | MinerU 轮询超时秒 |
| `BIUMIND_WIKI_PARSE_MAX_CONCURRENCY` | `4` | 并发 job 上限（OCR 单任务分钟级） |

## 本地开发

```bash
pip install -e ".[dev]"
pytest
```

## MVP 范围 + 排期

MVP：PDF + DOCX + XLSX + PPTX + EPUB + MD/TXT + HTML stdlib strip；PDF OCR
已接入（B1：自部署 MinerU，`wiki_parse/ocr.py`，默认关）。**未做**（见
`docs/BiuMind-Wiki-Gap-Analysis-DevPlan.md` S2 Phase 3 排期子项 +
`docs/BiuMind-Wiki-OCR-Plan.md` 不做清单）：

- MinerU 图片产物落库 + vision caption（D3 v1 丢弃图片只取文本，后续单独立项）
- pdfplumber 表格提取
- MOBI
- HTML readability-lxml / trafilatura 真 boilerplate 抽取
- 起手 UPDATE processing + CAS 防多 worker 重入
