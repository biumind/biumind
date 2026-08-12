#!/usr/bin/env bash
# BiuMind 开发环境引导
# 检查并安装所有必需工具
set -euo pipefail

bold() { printf "\033[1m%s\033[0m\n" "$*"; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$*"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$*"; }
fail() { printf "  \033[31m✗\033[0m %s\n" "$*"; }

need() {
  local cmd=$1
  local hint=${2:-"see project README"}
  if command -v "$cmd" >/dev/null 2>&1; then
    ok "$cmd $(${cmd} --version 2>&1 | head -1 || true)"
  else
    fail "$cmd not found — install with: $hint"
    return 1
  fi
}

bold "BiuMind dev bootstrap"
echo

bold "1. Core toolchain"
need go        "brew install go"
need dart      "brew install dart" || warn "dart optional if not touching Flutter"
need flutter   "https://docs.flutter.dev/get-started/install" || warn "flutter optional if not touching client"
need python3   "brew install python@3.12"
need uv        "curl -LsSf https://astral.sh/uv/install.sh | sh"
need node      "brew install node@20" || warn "node optional if not touching webclip"
need pnpm      "npm i -g pnpm" || warn "pnpm optional"

bold "2. Proto / RPC"
need buf       "brew install bufbuild/buf/buf"
need protoc-gen-go      "go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" || warn "auto-installable"
need protoc-gen-connect-go "go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest" || warn "auto-installable"

bold "3. DB / Lint / Misc"
need docker    "https://docs.docker.com/get-docker/"
need task      "brew install go-task"
need golangci-lint "brew install golangci-lint" || warn "optional but recommended"
need staticcheck   "go install honnef.co/go/tools/cmd/staticcheck@latest" || warn "optional"
need goose         "go install github.com/pressly/goose/v3/cmd/goose@latest" || warn "for db migrations"
need ripgrep   "brew install ripgrep"

bold "4. Project state"
[[ -f go.work ]] && ok "go.work exists" || fail "go.work missing"
[[ -d packages/proto ]] && ok "packages/proto present" || warn "proto schema not yet defined"
[[ -f deploy/docker-compose/.env ]] && ok ".env present" || warn "no .env yet — copy from deploy/docker-compose/.env.example"

# Milkdown WYSIWYG bundle —— notes/wiki 编辑器嵌入的 JS bundle。bundle 是
# gitignore 构建产物（不入仓），fresh clone 后必须构建，否则编辑器白屏
#（已踩过一次）。release CI 已建；这里给本地 dev 兜底。
bold "5. Editor bundle (Milkdown WYSIWYG)"
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  if [[ -d apps/client/editor-web ]]; then
    if (cd apps/client/editor-web && npm install --silent >/dev/null 2>&1 && npm run build >/dev/null 2>&1); then
      ok "editor bundle built (apps/client/assets/editor + web/editor)"
    else
      warn "editor-web build failed — 后续手跑 task editor-web:build，否则 notes/wiki 编辑器白屏"
    fi
  else
    warn "apps/client/editor-web 不存在，跳过 bundle 构建"
  fi
else
  warn "node/npm 缺失 —— 跳过 editor bundle 构建（notes/wiki 编辑器首跑前需 task editor-web:build）"
fi

echo
bold "Next steps:"
echo "  task proto:generate    # 生成 Go / Dart / TS 客户端"
echo "  task compose:up-infra  # 起本地依赖"
echo "  task hub:run           # 跑 Hub 服务"
echo "  task cli:install       # 装 biu CLI"
