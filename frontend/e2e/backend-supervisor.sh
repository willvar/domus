#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 0 ]]; then
  echo "usage: configure DOMUS_E2E_CONFIG, DOMUS_E2E_RUNTIME_ROOT and DOMUS_E2E_API_BASE" >&2
  exit 64
fi

config_path="${DOMUS_E2E_CONFIG:-config.yaml}"
runtime_root="${DOMUS_E2E_RUNTIME_ROOT:-tmp/dev}"
api_base_url="${DOMUS_E2E_API_BASE:-http://127.0.0.1:8088}"
api_base_url="${api_base_url%/}"

if [[ -z "$runtime_root" || "$runtime_root" == "/" ]]; then
  echo "refusing unsafe E2E runtime root: $runtime_root" >&2
  exit 64
fi

control_root="$runtime_root/run"
supervisor_pid_file="$control_root/e2e-backend-supervisor.pid"
generation_file="$control_root/e2e-backend-generation"
backend_binary="$control_root/domus-e2e-backend"
backend_pid=""
restart_requested=0
generation=0

mkdir -p "$control_root"
go build -o "$backend_binary" .
printf '%s\n' "$$" >"$supervisor_pid_file"

stop_backend() {
  if [[ -n "$backend_pid" ]]; then
    kill -TERM -- "$backend_pid" 2>/dev/null || true
  fi
}

request_restart() {
  restart_requested=1
  stop_backend
}

wait_for_backend() {
  backend_status=0
  while true; do
    set +e
    wait "$backend_pid"
    backend_status="$?"
    set -e
    if ! kill -0 "$backend_pid" 2>/dev/null; then
      return
    fi
  done
}

shutdown() {
  trap - EXIT INT TERM USR1
  stop_backend
  if [[ -n "$backend_pid" ]]; then
    wait "$backend_pid" 2>/dev/null || true
  fi
  rm -f -- "$supervisor_pid_file" "$generation_file" "$backend_binary"
}

trap request_restart USR1
trap 'exit 130' INT
trap 'exit 143' TERM
trap shutdown EXIT

while true; do
  # Playwright shuts web servers down by signalling their process group. Keep the
  # Domus Web in its own session so only this supervisor receives that signal.
  setsid "$backend_binary" dev -c "$config_path" --runtime-root "$runtime_root" &
  backend_pid="$!"

  backend_ready=0
  for _ in $(seq 1 1800); do
    if curl -fsS --max-time 1 "$api_base_url/auth" >/dev/null 2>&1; then
      backend_ready=1
      break
    fi
    if ! kill -0 "$backend_pid" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done

  if [[ "$backend_ready" -eq 1 ]]; then
    generation=$((generation + 1))
    generation_tmp="$generation_file.tmp.$$"
    printf '%s\n' "$generation" >"$generation_tmp"
    mv -f -- "$generation_tmp" "$generation_file"
  elif kill -0 "$backend_pid" 2>/dev/null; then
    echo "Domus E2E backend did not become ready within 180 seconds" >&2
    stop_backend
  fi

  wait_for_backend
  backend_pid=""

  if [[ "$restart_requested" -eq 1 ]]; then
    restart_requested=0
    continue
  fi
  exit "$backend_status"
done
