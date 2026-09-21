#!/usr/bin/env bash
# Local S3-compatible object store for Domus development.
#
# Mirrors the disposable SeaweedFS container used by scripts/test-oss-prefix.sh
# (same pinned image) but keeps a named, long-lived container for `make dev`:
#
#   scripts/local-s3.sh start|stop|status|reset
#
# The container listens on 127.0.0.1:8333 with fixed development credentials
# and a disk-backed data volume (LOCAL_S3_DATA_DIR, default tmp/local-s3) so
# large files fit. Data survives container restarts; `reset` deletes it.
set -euo pipefail

readonly seaweed_image='chrislusf/seaweedfs@sha256:43b768cd62b00d132439cda881b93fd1adebf1b315e996e794087743821d771d'
readonly container_name='domus-dev-s3'
readonly host_port='8333'
readonly bucket='domus-dev'
readonly access_key='domus-dev-access'
readonly secret_key='domus-dev-secret'
readonly repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly data_dir="${LOCAL_S3_DATA_DIR:-$repo_root/tmp/local-s3}"

command -v docker >/dev/null 2>&1 || { echo 'docker is required for the local development object store' >&2; exit 1; }

container_running() {
  [[ "$(docker inspect --format '{{.State.Running}}' "$container_name" 2>/dev/null || true)" == 'true' ]]
}

wait_ready() {
  local ready=0
  for _ in $(seq 1 120); do
    if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${host_port}/"; then
      ready=1
      break
    fi
    if ! container_running; then
      break
    fi
    sleep 0.25
  done
  if [[ "$ready" != '1' ]]; then
    docker logs "$container_name" >&2 || true
    echo 'SeaweedFS did not become ready' >&2
    exit 1
  fi
}

ensure_bucket() {
  if echo "s3.bucket.create -name $bucket" | docker exec -i "$container_name" weed shell >/dev/null 2>&1; then
    return
  fi
  echo "SeaweedFS is up but bucket ${bucket} could not be created" >&2
  echo "Create it manually: echo 's3.bucket.create -name ${bucket}' | docker exec -i ${container_name} weed shell" >&2
  exit 1
}

start() {
  if container_running; then
    ensure_bucket
    echo "local S3 already running: http://127.0.0.1:${host_port} bucket=${bucket}"
    return
  fi
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  mkdir -p "$data_dir"
  docker run --rm -d \
    --name "$container_name" \
    -v "${data_dir}:/data" \
    -p "${host_port}:8333" \
    -e AWS_ACCESS_KEY_ID="$access_key" \
    -e AWS_SECRET_ACCESS_KEY="$secret_key" \
    "$seaweed_image" \
    server -s3 -dir=/data -s3.port=8333 -master.volumeSizeLimitMB=32 -master.telemetry=false >/dev/null
  wait_ready
  ensure_bucket
  echo "local S3 started: http://127.0.0.1:${host_port} bucket=${bucket} data=${data_dir}"
}

stop() {
  if container_running; then
    docker stop --time 10 "$container_name" >/dev/null
    echo 'local S3 stopped'
  else
    echo 'local S3 not running'
  fi
}

status() {
  if container_running; then
    echo "running: http://127.0.0.1:${host_port} bucket=${bucket} data=${data_dir}"
  else
    echo "stopped (data=${data_dir})"
  fi
}

reset() {
  if container_running; then
    docker stop --time 10 "$container_name" >/dev/null
  fi
  rm -rf "$data_dir"
  echo "local S3 data removed: ${data_dir}"
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  reset) reset ;;
  *)
    echo "usage: $0 start|stop|status|reset" >&2
    exit 2
    ;;
esac
