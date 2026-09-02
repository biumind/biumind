# BiuMind Docker Compose 测试环境（dev/test 启动示例，非生产部署方案）
.DEFAULT_GOAL := help
SHELL := /bin/bash
COMPOSE := docker compose
ENV_FILE := .env

# 自动加载 .env，方便单独跑命令
ifneq (,$(wildcard $(ENV_FILE)))
include $(ENV_FILE)
export
endif

.PHONY: help
help: ## 显示这份帮助
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ─── 启动 ────────────────────────────────────────────
.PHONY: up-infra
up-infra: check-env ## 只拉基础设施（postgres/minio/nats）—— 本地开发：服务在宿主跑
	$(COMPOSE) up -d postgres minio nats minio-bootstrap
	@$(MAKE) wait-healthy

.PHONY: up
up: check-env ## 拉起完整栈（infra + 全部 Go 服务 + 前端 + workers）
	$(COMPOSE) up -d
	@$(MAKE) wait-healthy
	@echo "Site (统一入口): http://localhost:$${SITE_PORT:-8088}"

.PHONY: up-workers
up-workers: check-env ## 只拉 workers（依赖的 infra 由 depends_on 自动带起）
	$(COMPOSE) up -d worker-aigc worker-wiki-parse
	@$(MAKE) wait-healthy

.PHONY: up-all
up-all: up ## 别名：完整栈（= up）

# ─── 停止 ────────────────────────────────────────────
.PHONY: down
down: ## 停容器（保留数据 volume）
	$(COMPOSE) down

.PHONY: clean
clean: ## ⚠️  停容器并删除所有 volume（数据全清）
	@echo "WARNING: This will delete all volumes (postgres data, minio buckets, etc)"
	@read -p "Type 'yes' to continue: " ans && [ "$$ans" = "yes" ]
	$(COMPOSE) down -v --remove-orphans

.PHONY: restart
restart: down up ## down + up

# ─── 状态 / 日志 ─────────────────────────────────────
.PHONY: ps
ps: ## 列容器
	$(COMPOSE) ps

.PHONY: logs
logs: ## 跟所有服务的日志（CTRL-C 退出）
	$(COMPOSE) logs -f --tail=100 $(SVC)

.PHONY: tail
tail: ## 跟某个服务: make tail SVC=model-relay
	@test -n "$(SVC)" || (echo "Usage: make tail SVC=model-relay"; exit 1)
	$(COMPOSE) logs -f --tail=200 $(SVC)

.PHONY: health
health: ## 调每个服务的 /healthz
	@for port in 7001 7002 7003 7004 7007 7008 7009; do \
	  printf "port %s: " $$port; \
	  curl -sf -m 2 http://localhost:$$port/healthz 2>/dev/null && echo " ok" || echo " unhealthy"; \
	done

.PHONY: wait-healthy
wait-healthy: ## 等所有 healthcheck 通过（最多 90s）
	@echo "Waiting for services to become healthy..."
	@for i in $$(seq 1 18); do \
	  unhealthy=$$($(COMPOSE) ps --format json 2>/dev/null | grep -c '"Health":"unhealthy"' || true); \
	  starting=$$($(COMPOSE) ps --format json 2>/dev/null | grep -c '"Health":"starting"' || true); \
	  if [ "$$unhealthy" = "0" ] && [ "$$starting" = "0" ]; then echo "all healthy"; exit 0; fi; \
	  printf "."; sleep 5; \
	done; echo " timeout"; $(MAKE) ps; exit 1

# ─── 镜像 ────────────────────────────────────────────
.PHONY: build-images
build-images: ## 构建所有 BiuMind 服务镜像（docker.io/biumind/<name>:dev，配合 make up-local）
	@# build context 一律为**仓库根** ../.. —— 所有服务 Dockerfile 都
	@# `COPY packages/go-sdk/biu`（brain / runtime 还 COPY apps/cli/biu），
	@# 窄 context（../../services/<name>）取不到这些路径会 COPY 失败。
	@# 每项格式 image:dir —— 仅 app_center 目录名与镜像名(app-center)不一致；
	@# 只列**compose 里用到且有 Dockerfile** 的服务（sandbox/deploy 有
	@# Dockerfile 但不在 compose；presence/billing 尚未实现）。
	@set -e; for pair in \
	  aigc:aigc app-center:app_center authz:authz brain:brain channels:channels identity:identity \
	  model-relay:model-relay realtime:realtime runtime:runtime; do \
	  img=$${pair%%:*}; dir=$${pair##*:}; \
	  echo "→ building $$img (services/$$dir)"; \
	  docker build -t biumind/$$img:dev -f ../../services/$$dir/Dockerfile ../.. || exit 1; \
	done

.PHONY: authz-eval
authz-eval: ## 测一条 authz 决策: make authz-eval PRINCIPAL=user:u1 ACTION=wiki:Page::read RESOURCE=page:p1
	@test -n "$(PRINCIPAL)" && test -n "$(ACTION)" && test -n "$(RESOURCE)" || \
	 (echo "Usage: make authz-eval PRINCIPAL=user:u1 ACTION=wiki:Page::read RESOURCE=page:p1"; exit 1)
	curl -sX POST http://localhost:$${AUTHZ_PORT:-7009}/v1/authz/check \
	  -H 'Content-Type: application/json' \
	  -d '{"principal":"$(PRINCIPAL)","action":"$(ACTION)","resource":"$(RESOURCE)"}' | jq

.PHONY: pull-images
pull-images: ## 拉外部依赖镜像（infra）
	$(COMPOSE) pull postgres minio nats

.PHONY: up-local
up-local: ## 用本地 build-images 的镜像起栈（docker.io/biumind/<name>:dev）
	$(MAKE) BIUMIND_REGISTRY=docker.io BIUMIND_TAG=dev up

# ─── 数据库 ──────────────────────────────────────────
.PHONY: psql
psql: ## 进 postgres shell
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-biumind} -d $${POSTGRES_DB:-biu_core}

.PHONY: backup-pg
backup-pg: ## 备份 Postgres 到 ./backups/
	@mkdir -p backups
	$(COMPOSE) exec -T postgres pg_dump -U $${POSTGRES_USER:-biumind} -d $${POSTGRES_DB:-biu_core} -F c \
	  > backups/biu_core_$$(date +%Y%m%d_%H%M%S).dump
	@ls -lh backups/ | tail -5

.PHONY: restore-pg
restore-pg: ## 从备份恢复: make restore-pg FILE=backups/xxx.dump
	@test -n "$(FILE)" || (echo "Usage: make restore-pg FILE=backups/xxx.dump"; exit 1)
	cat $(FILE) | $(COMPOSE) exec -T postgres pg_restore -U $${POSTGRES_USER:-biumind} \
	  -d $${POSTGRES_DB:-biu_core} --clean --if-exists

# ─── 实用工具 ────────────────────────────────────────
.PHONY: nats-streams
nats-streams: ## 列出 NATS JetStream
	$(COMPOSE) exec nats nats stream list

.PHONY: minio-console
minio-console: ## 打开 MinIO 控制台
	@open http://localhost:$${MINIO_CONSOLE_PORT:-9001} 2>/dev/null || \
	 xdg-open http://localhost:$${MINIO_CONSOLE_PORT:-9001} 2>/dev/null || \
	 echo "MinIO Console: http://localhost:$${MINIO_CONSOLE_PORT:-9001}"

# ─── 守门员 ──────────────────────────────────────────
.PHONY: check-env
check-env:
	@test -f $(ENV_FILE) || (echo "ERROR: missing $(ENV_FILE). Copy from .env.example."; exit 1)
	@grep -q '^BIUMIND_MASTER_KEY=' $(ENV_FILE) || (echo "ERROR: BIUMIND_MASTER_KEY not set in .env"; exit 1)

.PHONY: lint
lint: ## 校验所有 compose 文件
	$(COMPOSE) config -q && echo "compose: ok"

.PHONY: fresh
fresh: clean up-infra ## 一键重建（清数据+起 infra）
