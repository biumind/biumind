# BiuMind 顶层 Makefile —— 跨语言/跨模块协作命令归集到这里。
# 单一仓库内多模块各有 go.mod/pubspec/package.json，本 Makefile 只放需要顶层视角的命令。

.PHONY: help schema-validate \
        editor-web-install editor-web-dev editor-web-build editor-web-test editor-web-typecheck

help:
	@echo "BiuMind targets:"
	@echo "  schema-validate       校验 schema/sdk/v1 下所有 JSON Schema + fixtures"
	@echo "  editor-web-install    npm install in apps/client/editor-web"
	@echo "  editor-web-dev        Vite dev server at http://localhost:5174 (standalone)"
	@echo "  editor-web-build      构建 bundle 并同步到 apps/client/{web,assets}/editor"
	@echo "  editor-web-test       vitest (round-trip + bridge)"
	@echo "  editor-web-typecheck  tsc --noEmit"

# SDK Protocol v1 JSON Schema 校验：编译每个 schema 文件，再用 fixtures/ 实例对应校验。
# fixtures 文件需含 "$$schema" 字段指向所属 schema（相对路径或完整 $$id）。
schema-validate:
	go run ./tools/schema-validate

# ─── editor-web (Milkdown WYSIWYG bundle) ─────────────────────
# Flutter 客户端嵌入的 markdown 富文本编辑器。Web 端走 iframe
# （apps/client/web/editor/），原生端走 inappwebview
# （apps/client/assets/editor/），两端共享同一份 dist。
# 首次拉仓后必须先 `make editor-web-install && make editor-web-build`，
# 否则 /wiki/p/:pid/pages/:id 编辑模式 webview 加载 404。
editor-web-install:
	cd apps/client/editor-web && npm install

editor-web-dev:
	cd apps/client/editor-web && npm run dev

editor-web-build:
	cd apps/client/editor-web && npm run build

editor-web-test:
	cd apps/client/editor-web && npm run test

editor-web-typecheck:
	cd apps/client/editor-web && npm run typecheck
