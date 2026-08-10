#!/usr/bin/env bash
# BiuMind 架构不变量校验（CI / pre-commit）
# I1-I9（后端）+ C1-C11（端）的可静态扫描部分
#
# ─── 深度清理工作项（ratchet 止血后，独立收敛）─────────────────────────
# C5: 现 21 处 Platform.isXXX 散在业务代码（基线 .c5_baseline=21）。
#     目标: 全部收敛到 apps/client/lib/platform/，基线降到 0。
#     见: grep -rn 'Platform.is' apps/client/lib --include='*.dart' | grep -v '/platform/'
# T1: 现 54 处 Color(0xFF...) 硬编码（基线 .theme_t1_baseline=54）。
#     目标: 全部换 BiuColors token / Theme.of(context).extension<BiuColors>()，基线降到 0。
#     见: grep -rn 'Color(0x' apps/client/lib --include='*.dart' | grep -v 'app/theme/'
#     允许列表: app/theme/ (token 源头) + // theme-ignore: legacy 标记行。
# 基线抬升 = 止血锁增不增（2026-07-27 已做）；深度收敛 = 真还债（待排期）。
set -euo pipefail

# 始终从仓库根目录运行（脚本所在目录的祖父目录）
cd "$(dirname "$0")/../.." || exit 1

bold() { printf "\033[1m%s\033[0m\n" "$*"; }
fail() { printf "  \033[31m✗\033[0m %s\n" "$*"; FAILED=1; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$*"; }

FAILED=0

# I1（旧）"不要 WebSocket" —— 已移除。
#
# 该检查实现的是 v1.5 之前的设计(实时数据全走 SSE、Envoy 拒绝 WebSocket
# upgrade)。v1.5 起双向流改走 **WebSocket SDK Protocol**(proto3 over WS),
# SSE 仅保留给 Realtime 多 topic 通知 —— 见 CLAUDE.md 硬约束。命中的
# code_ws.go / ws.go / agentplane/ingress.go / wiki/syncws 全是合法 SDK
# Protocol 实现,封禁 WebSocket 与现行架构相反,故删此条而非反转(避免擅自
# 新造正向不变量)。
#
# ⚠️ 文档债:docs/BiuMind-Technical-Architecture.md §1.2 的 I1/I8 仍写
#    "实时数据走 SSE 而非 WebSocket",与 CLAUDE.md 矛盾,待与文档所有者
#    一并校订(属跨服务边界/架构红线,不在本脚本擅改)。

bold "I6: 模型调用必过 Hub"
# 业务服务（不含 Hub 自己）不允许直接 import provider SDK
for svc in services/runtime services/brain services/channels services/billing; do
  [[ -d $svc ]] || continue
  if grep -rn --include='*.go' -E '"github.com/(sashabaranov/go-openai|liushuangls/go-anthropic)"' $svc 2>/dev/null | grep -v "_test.go"; then
    fail "I6: $svc 直接 import LLM SDK；必须走 Hub"
  fi
done
ok "I6: 业务服务无直连 LLM"

# I7(旧）"AG-UI 自定义事件命名规范" —— 已退役。
#
# AG-UI 于 v1.5 删除,事件流改走 SDK Protocol(proto3/JSON wire over WS),不再有
# AG-UI `CUSTOM` 事件;BiuMind 扩展字段统一 `biumind_` 前缀。新 I7 语义(事件流
# 符合 SDK Protocol、改 proto/schema 后 proto:generate 同步三端)属流程性,由
# proto CI 守,不在本静态脚本覆盖。见 BiuMind-Technical-Architecture.md §1.2 I7
# + §3.3 + BiuMind-Agent-Plane-Design.md §6。
# (原 grep 还误用了 grep -E 不支持的 (?!) lookahead,实际从不命中,等同空转。)

bold "C5: platform-specific 代码不散落 [ratchet]"
# 业务代码不应 if (Platform.isXXX) ；该收敛到 lib/platform/。当前有既存违规
# (main.dart / biu_daemon_manager / login_shell_env / features/code 等),一次性
# 重构是独立工作项;此处用 ratchet 基线锁住"只减不增"—— 新增立即报错,迁移走
# 基线下降。基线减小后跑:echo $NEW_COUNT > tools/scripts/.c5_baseline
C5_BASELINE_FILE=tools/scripts/.c5_baseline
C5_BASELINE=$([[ -f "$C5_BASELINE_FILE" ]] && cat "$C5_BASELINE_FILE" || echo 9999)
C5_COUNT=$( ( grep -rn --include='*.dart' \
  -E 'Platform\.is(IOS|Android|MacOS|Windows|Linux)' \
  apps/client/lib 2>/dev/null \
  | grep -v "/platform/" \
  || true ) | wc -l | tr -d ' ')
if [[ $C5_COUNT -gt $C5_BASELINE ]]; then
  fail "C5: Platform.isXXX 回归 (现 $C5_COUNT 处 > 基线 $C5_BASELINE) — 新增的请收敛到 lib/platform/"
  echo "    见: grep -rn 'Platform.is' apps/client/lib --include='*.dart' | grep -v '/platform/'"
elif [[ $C5_COUNT -lt $C5_BASELINE ]]; then
  ok "C5: Platform.isXXX 下降 ($C5_COUNT < 基线 $C5_BASELINE) — 更新基线: echo $C5_COUNT > $C5_BASELINE_FILE"
else
  ok "C5: Platform.isXXX 持平基线 ($C5_COUNT,收敛进行中)"
fi

bold "I3: trace_id 透传 (HTTP middleware)"
if [[ -f packages/go-sdk/biu/otel/otel.go ]]; then
  ok "I3: otel SDK 实现已就位"
else
  printf "  \033[33m!\033[0m I3: otel SDK 未实现（P0.3 待补）\n"
fi

bold "I4: app_center mutation 必有 events 行"
# 凡是 SQL 直接 INSERT / UPDATE / DELETE 一个 app_center.* 实体表
# 的 Go 文件，同一文件必须出现 events.Write — 否则 mutation 流向
# Realtime / outbox 时会丢事件。豁免：cmd/main.go 不做业务写入，
# events 包自己是事件层，invocations 表只追加（caller 已在调用栈
# 的 events.Write 处审计），webhook / dispatcher 的 invocations
# audit row 不计 mutation（它们是 read-side 影像）。
ac_missing=0
ac_files=$(grep -rln --include='*.go' \
  -E '(INSERT INTO|UPDATE|DELETE FROM)\s+app_center\.(installations|scheduler_jobs|sidebar_layouts|agent_apps|apps)\b' \
  services/app_center 2>/dev/null \
  | grep -v "_test.go" \
  | grep -v "/cmd/" \
  | grep -v "/internal/events/" \
  || true)
for f in $ac_files; do
  if ! grep -q "events\.Write" "$f" 2>/dev/null; then
    fail "I4: $f 修改 app_center.* 但未写 events"
    ac_missing=1
  fi
done
if [[ $ac_missing -eq 0 ]]; then
  ok "I4: app_center.* mutation 全部伴随 events 写入"
fi

bold "I5: App actions 走 Runtime tools 唯一入口"
# Runtime apptools 包以外，业务代码不许直接 import biuapp.Registry 的
# Invoke 来调 App actions（绕过 tools.Registry 等于绕过 Authz + 审计）。
# 豁免：runtime/internal/apptools/* 是桥本身；app_center/* 是 Registry 宿主。
if grep -rn --include='*.go' \
   -E 'biuapp\.Registry.*\.Invoke\(' \
   services 2>/dev/null \
   | grep -v "/internal/apptools/" \
   | grep -v "/services/app_center/" \
   | grep -v "_test.go"; then
  fail "I5: 发现绕过 apptools 直接调 biuapp.Registry.Invoke"
else
  ok "I5: App invoke 路径唯一"
fi

bold "I6: App SDK 不持有 LLM 凭据"
# bundled apps 不允许直接 import 任何 LLM SDK；必须走 Hub via Deps
if grep -rln --include='*.go' \
  -E '"github.com/(sashabaranov/go-openai|liushuangls/go-anthropic|anthropics/anthropic-sdk-go)"' \
  packages/go-sdk/biu/biuapp 2>/dev/null \
  | grep -v "_test.go"; then
  fail "I6 (App SDK): bundled app 直接 import LLM SDK"
else
  ok "I6 (App SDK): bundled app 无直连 LLM"
fi

# ─── 主题不变量 (T1-T8 docs/BiuMind-Theme-System-Design.md §7) ─────────
#
# T1: 业务代码禁止硬编码 hex 字面量 — Color(0xFF...) 仅允许在 token / palette
#     源头出现。这是设计令牌系统的命脉(否则切色板不跟)。
# T2: 新代码禁止 import biu_tokens.dart (老 shim,只为兼容存在,逐步迁移)。

bold "T1: 业务代码禁止硬编码 Color(0xFF...) [ratchet]"
# 允许列表:
#   * apps/client/lib/app/theme/  (token 源头)
#   * 老代码标记了 // theme-ignore: legacy 的行 (临时豁免)
#
# Ratchet 策略: 计数只能 ≤ 基线 (tools/scripts/.theme_t1_baseline)。
# 新增 hex 必报错,迁移走基线下降。基线减小后跑:
#   echo $NEW_COUNT > tools/scripts/.theme_t1_baseline
T1_BASELINE_FILE=tools/scripts/.theme_t1_baseline
T1_BASELINE=$([[ -f "$T1_BASELINE_FILE" ]] && cat "$T1_BASELINE_FILE" || echo 9999)
T1_COUNT=$( ( grep -rn --include='*.dart' \
  -E 'Color\(0x[0-9a-fA-F]{6,8}\)' \
  apps/client/lib 2>/dev/null \
  | grep -v "apps/client/lib/app/theme/" \
  | grep -v "// theme-ignore: legacy" \
  || true ) | wc -l | tr -d ' ')
if [[ $T1_COUNT -gt $T1_BASELINE ]]; then
  fail "T1: hex 字面量回归 (现 $T1_COUNT 处 > 基线 $T1_BASELINE) — 新增的请改用 BiuColors token"
  echo "    见: grep -rn 'Color(0x' apps/client/lib --include='*.dart' | grep -v 'app/theme/'"
elif [[ $T1_COUNT -lt $T1_BASELINE ]]; then
  ok "T1: hex 字面量下降 ($T1_COUNT < 基线 $T1_BASELINE) — 更新基线: echo $T1_COUNT > $T1_BASELINE_FILE"
else
  ok "T1: hex 字面量持平基线 ($T1_COUNT,迁移进行中)"
fi

bold "T2: 新代码禁止 import biu_tokens.dart"
# biu_tokens.dart 是 deprecated shim — 只允许 theme/ 内部互引 + 兼容入口。
# 新代码用 Theme.of(context).extension<BiuColors>() 走主题系统。
if grep -rn --include='*.dart' \
  "import.*'.*app/theme/biu_tokens.dart'" \
  apps/client/lib 2>/dev/null \
  | grep -v "apps/client/lib/app/theme/"; then
  fail "T2: 发现新代码 import biu_tokens.dart (用 BiuColors 替代)"
else
  ok "T2: 无新代码 import biu_tokens.dart"
fi

# ─── 编码模块不变量 (Code-I1..I8 — docs/BiuMind-Code-Design.md §不变量) ──────
#
# 仅接入 **静态可查、零误报** 的 4 条;其余属单测/流程性,不在本脚本覆盖:
#   Code-I3 (远控不自造 relay)     — 需理解包语义,易误报,缓接
#   Code-I5 (远控写受 tool_policy) — 单测 (复用 v3 R6.3 链路)
#   Code-I7 (frame 改后 proto:generate) — 流程性,靠 proto CI
#   Code-I8 (PTY 走 loopback)      — 单测/集成

bold "Code-I1: Flutter 不直 import LLM SDK (AI commit / 任务名必走 model-relay)"
# 仅匹配真·LLM SDK 的 pub 包 import;内部 core/llm/ 目录(provider catalog、
# UI 适配层)不算。AI 文案生成在 biu CLI 端走 client.RelayProvider,Flutter 不碰。
if grep -rnE "import\s+'package:(dart_openai|openai_dart|openai|anthropic[a-z_]*|dart_anthropic|langchain[a-z_]*|google_generative_ai|ollama[a-z_]*)/" \
     apps/client/lib --include='*.dart' 2>/dev/null; then
  fail "Code-I1: Flutter 直接 import LLM SDK;AI 文案必须走 model-relay"
else
  ok "Code-I1: Flutter 无直连 LLM SDK"
fi

bold "Code-I2: services/* 不直 spawn 用户进程 (PTY 在 biu CLI 内)"
# 业务服务不许 spawn agent / shell 进程 —— 用户进程的 PTY 归 biu CLI
# (apps/cli/biu/pkg/biumindkit/code)。services 远控只透传 frame,不落地执行。
if grep -rnE 'exec\.Command(Context)?\(\s*"(claude|codex|biu)"' \
     services --include='*.go' 2>/dev/null | grep -v "_test.go"; then
  fail "Code-I2: services 直接 spawn agent 进程;PTY 必须在 biu CLI 内"
else
  ok "Code-I2: services 无直 spawn agent 进程"
fi

bold "Code-I4: brain 无编码任务表/包 (task/session/artifact 不上云持久化)"
# 编码任务 100% 本地(D4),Drift 即唯一 SoT。brain 不得重建编码任务包;
# code schema 由 migration 00046_drop_code_tasks.sql DROP。
if [[ -d services/brain/internal/code ]]; then
  fail "Code-I4: services/brain/internal/code 包仍存在;编码任务不应在 brain 持久化"
else
  ok "Code-I4: brain 无编码任务表/包"
fi

bold "Code-I6: 已废弃 codeSync 代码从生产路径移除"
# 允许:迁移里的 DROP TABLE 清理语句、注释、测试文件。
if grep -rnE "code_task_outbox|code_sync_cursors|CodeTaskOutbox|CodeSyncCursor" \
     apps/client/lib services packages \
     --include='*.dart' --include='*.go' 2>/dev/null \
     | grep -vE "DROP TABLE|^[^:]*:[0-9]+:[[:space:]]*//|废弃|已删|deprecated" \
     | grep -v "_test"; then
  fail "Code-I6: 生产代码仍引用已废弃的 codeSync outbox/cursor"
else
  ok "Code-I6: 无废弃 codeSync 残留"
fi

bold "C-ROT: router refreshListenable 桥禁直听 hubCredentialsProvider (token 轮换不闪)"
# _RouterListenable 必须听 isAuthenticatedProvider (bool derived), 不能直接听
# hubCredentialsProvider 原值 —— 否则 token 每小时轮换 (resolve() 返新对象 + 无 ==)
# 会让 GoRouter refresh → 整路由栈 rebuild → 所有页面闪 (commit 7826bbf1 修的 bug)。
# 数据 provider 侧用 select(endpoint) 解耦 (commit bc20d51e); router 侧用 bool。
if grep -rnE 'listen<HubCredentials\?>' apps/client/lib/app/router.dart 2>/dev/null; then
  fail "C-ROT: router 仍直接 listen hubCredentialsProvider 原值; refreshListenable 桥应用 isAuthenticatedProvider (bool)"
  echo "    见: lib/app/router.dart _RouterListenable + lib/services/auth_service.dart isAuthenticatedProvider"
else
  ok "C-ROT: router refreshListenable 桥未直听 hubCredentialsProvider 原值"
fi

bold "C-ISO: 本地 per-user 表必须有 ownerKey/scope 隔离列"
# 防本地数据跨账号泄露（docs/BiuMind-Local-Data-Isolation-Design.md §1/§3）。
# 笔记域曾在 Phase 30 做 chat 隔离时被整体漏掉，重新部署 + 相同邮箱重新注册后
# 桌面端直接展示上一账号笔记 —— 本条把「per-user 本地表必须隔离」沉淀为 CI 护栏。
#
# 两道检查：
#  (1) 登记为「需隔离」的 per-user 表（云端 SoT 本地镜像）必须在 db.dart 声明
#      ownerKey 或 scope 列 —— 防有人删列导致某表退回无隔离。
#  (2) db.dart 每张 Drift 表都必须显式归入 SCOPED_REQUIRED 或 EXEMPT —— 新增表
#      若漏登记，全量表数 ≠ SCOPED+EXEMPT 即 FAIL，逼开发者定夺「新表是否
#      per-user、要不要加隔离」。这正是防「笔记式遗漏」的核心机制。
#
# SCOPED：chat 五表 + chatSyncState/chatOutbox（scope 列）+ notes 五表。
# EXEMPT：wiki/aigc（登出 wipe 兜底）/ code（本地 SoT 零云同步）/ sse（登出
#         clearAll）/ rss（用 scopeId=JWT sub 等价隔离，列名不同故不进 SCOPED）。
SCOPED_REQUIRED="ChatThreadsV2 ChatMessagesV2 ChatContentBlocks MessageReactionsV2 ChatSessions ChatSyncState ChatOutbox NoteNotebooks NoteNotes NoteTags NoteNoteTags NoteOutbox"
EXEMPT="WikiProjects WikiPages WikiBlocks WikiOutbox CodeTasks CodeProjects CodeTaskArtifacts SseCursors AigcTasks RssFeedsCache RssEntriesCache"
# db.dart 中声明了隔离列（ownerKey/scope）的表名 —— 用 awk 按最近一个
# `class X extends Table` 归属列定义（表块连续，归属可靠）。awk 自带关联数组，
# 不依赖 bash4（macOS 默认 bash3 兼容）。scopeId 不算（后跟字母 I，正则排除）。
SCOPED_DECLARED=$(awk '
  /^class [A-Za-z0-9_]+ extends Table/ { cur=$2 }
  /TextColumn get (ownerKey|scope)[^A-Za-z]/ { if (cur) seen[cur]=1 }
  END { for (t in seen) print t }
' apps/client/lib/data/local/db.dart)
ALL_TABLES=$(awk '/^class [A-Za-z0-9_]+ extends Table/ { print $2 }' apps/client/lib/data/local/db.dart)
iso_fail=0
# (1) 需隔离表确实声明了隔离列
for t in $SCOPED_REQUIRED; do
  if ! echo "$SCOPED_DECLARED" | grep -qxF "$t"; then
    fail "C-ISO: per-user 表 $t 缺 ownerKey/scope 隔离列（db.dart）—— 见 docs/BiuMind-Local-Data-Isolation-Design.md §3"
    iso_fail=1
  fi
done
# (2) 每张表必须显式分类（SCOPED 或 EXEMPT），新增表漏登记即 FAIL
for t in $ALL_TABLES; do
  if ! echo "$SCOPED_REQUIRED $EXEMPT" | grep -qw "$t"; then
    fail "C-ISO: 新表 $t 未分类 —— per-user 表请加 ownerKey 并入 SCOPED_REQUIRED；全局/本地SoT/已wipe兜底表并入 EXEMPT"
    iso_fail=1
  fi
done
if [[ $iso_fail -eq 0 ]]; then
  scoped_n=$(echo $SCOPED_REQUIRED | wc -w | tr -d ' ')
  exempt_n=$(echo $EXEMPT | wc -w | tr -d ' ')
  ok "C-ISO: per-user 表隔离列齐全（SCOPED $scoped_n 张 + EXEMPT $exempt_n 张）"
fi

echo
if [[ $FAILED -eq 0 ]]; then
  bold "✓ 不变量检查全部通过"
  exit 0
else
  bold "✗ 不变量检查失败"
  exit 1
fi
