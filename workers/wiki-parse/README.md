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

## 本地开发

```bash
pip install -e ".[dev]"
pytest
```

## MVP 范围 + 排期

MVP：PDF + DOCX + XLSX + PPTX + EPUB + MD/TXT + HTML stdlib strip。**未做**（见
`docs/BiuMind-Wiki-Gap-Analysis-DevPlan.md` S2 Phase 3 排期子项）：

- pdfplumber 表格提取 / MinerU 外置服务（扫描版 / 复杂表格）
- 图片 OCR（走 B1 MinerU 方案另行立项，不用 pytesseract）
- MOBI
- HTML readability-lxml / trafilatura 真 boilerplate 抽取
- 起手 UPDATE processing + CAS 防多 worker 重入
