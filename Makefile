.DEFAULT_GOAL := help

BACKEND_DIR := backend
ENV_FILE    := $(BACKEND_DIR)/.env

define with_env
	set -a; [ -f $(ENV_FILE) ] && . $(ENV_FILE); set +a; $(1)
endef

define ensure_dirs
	@mkdir -p $(BACKEND_DIR)/data/uploads $(BACKEND_DIR)/data/temp
endef

.PHONY: help api worker dev

help: ## 显示可用命令
	@echo ""
	@grep -E '^[a-zA-Z0_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "快速开始: make dev"
	@echo ""

api: ## 启动 API
	$(ensure_dirs)
	@$(call with_env,cd $(BACKEND_DIR) && go run ./cmd/api)

worker: ## 启动转码 Worker
	$(ensure_dirs)
	@$(call with_env,cd $(BACKEND_DIR) && go run ./cmd/video-worker)

dev: ## 同时启动 API + Worker
	$(ensure_dirs)
	@trap 'kill 0' INT TERM; \
	$(call with_env,cd $(BACKEND_DIR) && go run ./cmd/api) & \
	$(call with_env,cd $(BACKEND_DIR) && go run ./cmd/video-worker) & \
	wait
