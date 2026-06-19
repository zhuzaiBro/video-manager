#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$ROOT_DIR/backend"
ENV_FILE="$BACKEND_DIR/.env"
RUN_DIR="$ROOT_DIR/.run"
LOG_DIR="$ROOT_DIR/logs"
BIN_DIR="$BACKEND_DIR/bin"

API_BIN="$BIN_DIR/api"
WORKER_BIN="$BIN_DIR/video-worker"
API_PID="$RUN_DIR/api.pid"
WORKER_PID="$RUN_DIR/worker.pid"
API_LOG="$LOG_DIR/api.log"
WORKER_LOG="$LOG_DIR/worker.log"

GO_BIN=""

load_env() {
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
  fi
  GO_BIN="$(resolve_go_bin)" || true
}

resolve_go_bin() {
  local candidates=()

  if [[ -n "${GO_BIN:-}" ]]; then
    candidates+=("$GO_BIN")
  fi

  candidates+=(
    /usr/local/btgo/bin/go
    /usr/local/go/bin/go
    go
  )

  local c
  for c in "${candidates[@]}"; do
    if [[ "$c" == /* && -x "$c" ]]; then
      echo "$c"
      return 0
    fi
    if command -v "$c" &>/dev/null; then
      command -v "$c"
      return 0
    fi
  done

  return 1
}

ensure_dirs() {
  mkdir -p "$RUN_DIR" "$LOG_DIR" "$BACKEND_DIR/data/uploads" "$BACKEND_DIR/data/temp" "$BIN_DIR"
}

is_running() {
  local pid_file=$1
  [[ -f "$pid_file" ]] || return 1
  local pid
  pid="$(cat "$pid_file")"
  kill -0 "$pid" 2>/dev/null
}

start_proc() {
  local name=$1
  local bin=$2
  local pid_file=$3
  local log_file=$4

  if is_running "$pid_file"; then
    echo "[$name] already running (pid $(cat "$pid_file"))"
    return 0
  fi

  echo "[$name] starting..."
  nohup "$bin" >>"$log_file" 2>&1 &
  echo $! >"$pid_file"
  sleep 1

  if is_running "$pid_file"; then
    echo "[$name] started (pid $(cat "$pid_file"), log $log_file)"
  else
    echo "[$name] failed to start, see $log_file" >&2
    rm -f "$pid_file"
    return 1
  fi
}

stop_proc() {
  local name=$1
  local pid_file=$2

  if ! is_running "$pid_file"; then
    echo "[$name] not running"
    rm -f "$pid_file"
    return 0
  fi

  local pid
  pid="$(cat "$pid_file")"
  echo "[$name] stopping (pid $pid)..."
  kill "$pid" 2>/dev/null || true

  for _ in $(seq 1 10); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "[$name] stopped"
      return 0
    fi
    sleep 1
  done

  echo "[$name] force killing..."
  kill -9 "$pid" 2>/dev/null || true
  rm -f "$pid_file"
  echo "[$name] stopped"
}

build() {
  load_env

  if [[ -z "$GO_BIN" ]]; then
    echo "[build] go not found, tried: GO_BIN, /usr/local/btgo/bin/go, /usr/local/go/bin/go, PATH" >&2
    exit 1
  fi

  echo "[build] compiling with $GO_BIN..."
  cd "$BACKEND_DIR"
  "$GO_BIN" mod tidy
  "$GO_BIN" build -o "$API_BIN" ./cmd/api
  "$GO_BIN" build -o "$WORKER_BIN" ./cmd/video-worker
  echo "[build] done: $API_BIN, $WORKER_BIN"
}

do_start() {
  ensure_dirs
  load_env

  if [[ ! -x "$API_BIN" || ! -x "$WORKER_BIN" ]]; then
    build
  fi

  start_proc "api" "$API_BIN" "$API_PID" "$API_LOG"
  start_proc "worker" "$WORKER_BIN" "$WORKER_PID" "$WORKER_LOG"
}

do_stop() {
  stop_proc "worker" "$WORKER_PID"
  stop_proc "api" "$API_PID"
}

do_restart() {
  do_stop
  do_start
}

do_redeploy() {
  ensure_dirs
  load_env

  if [[ -d "$ROOT_DIR/.git" ]]; then
    echo "[redeploy] git pull..."
    git -C "$ROOT_DIR" pull --ff-only
  fi

  build
  do_stop
  do_start
  echo "[redeploy] complete"
}

do_status() {
  for item in "api:$API_PID" "worker:$WORKER_PID"; do
    name="${item%%:*}"
    pid_file="${item##*:}"
    if is_running "$pid_file"; then
      echo "[$name] running (pid $(cat "$pid_file"))"
    else
      echo "[$name] stopped"
    fi
  done
}

usage() {
  cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  start      编译并后台启动 api + worker
  stop       停止 api + worker
  restart    重启 api + worker
  redeploy   git pull + 编译 + 重启
  status     查看运行状态
  build      仅编译
  logs       跟踪 api/worker 日志

Examples:
  ./app.sh start
  ./app.sh redeploy
  ./app.sh logs api
EOF
}

do_logs() {
  local target=${1:-all}
  case "$target" in
    api) tail -f "$API_LOG" ;;
    worker) tail -f "$WORKER_LOG" ;;
    all) tail -f "$API_LOG" "$WORKER_LOG" ;;
    *) echo "unknown log target: $target (api|worker|all)" >&2; exit 1 ;;
  esac
}

cmd=${1:-}
case "$cmd" in
  start) do_start ;;
  stop) do_stop ;;
  restart) do_restart ;;
  redeploy) do_redeploy ;;
  status) do_status ;;
  build) build ;;
  logs) do_logs "${2:-all}" ;;
  help | -h | --help) usage ;;
  *)
    usage
    exit 1
    ;;
esac
