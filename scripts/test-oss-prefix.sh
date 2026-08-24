#!/usr/bin/env bash
set -euo pipefail

readonly seaweed_image='chrislusf/seaweedfs@sha256:43b768cd62b00d132439cda881b93fd1adebf1b315e996e794087743821d771d'
readonly container_name="domus-seaweedfs-prefix-test-$$"

command -v docker >/dev/null 2>&1 || { echo 'docker is required for the SeaweedFS integration test' >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo 'curl is required for the SeaweedFS integration test' >&2; exit 1; }
[[ "$container_name" =~ ^domus-seaweedfs-prefix-test-[0-9]+$ ]]

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/.." && pwd -P)"
[[ -f "$repo_root/go.mod" ]] || { echo "invalid repository root: $repo_root" >&2; exit 1; }

container_id="$(docker run --rm -d \
  --name "$container_name" \
  --tmpfs /data:rw,nosuid,nodev,size=64m \
  -p 127.0.0.1::8333 \
  -e AWS_ACCESS_KEY_ID='domus-test-access' \
  -e AWS_SECRET_ACCESS_KEY='domus-test-secret' \
  "$seaweed_image" \
  server -s3 -dir=/data -s3.port=8333 -master.volumeSizeLimitMB=32 -master.telemetry=false)"
[[ "$container_id" =~ ^[0-9a-f]{64}$ ]] || { echo 'docker returned an invalid container ID' >&2; exit 1; }

cleanup() {
  status="$?"
  trap - EXIT INT TERM
  inspected_name="$(docker inspect --format '{{.Name}}' "$container_id" 2>/dev/null || true)"
  if [[ "$inspected_name" == "/$container_name" ]]; then
    docker stop --time 10 "$container_id" >/dev/null
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

mapped_port="$(docker port "$container_id" 8333/tcp | sed -n '1s/.*://p')"
[[ "$mapped_port" =~ ^[0-9]+$ ]] || { echo 'failed to resolve the SeaweedFS host port' >&2; exit 1; }

ready=0
for _ in $(seq 1 120); do
  if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${mapped_port}/"; then
    ready=1
    break
  fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "$container_id")" != 'true' ]]; then
    break
  fi
  sleep 0.25
done
if [[ "$ready" != '1' ]]; then
  docker logs "$container_id" >&2
  echo 'SeaweedFS did not become ready' >&2
  exit 1
fi

cd -- "$repo_root"
DOMUS_SEAWEEDFS_INTEGRATION=1 \
DOMUS_TEST_S3_ENDPOINT="http://127.0.0.1:${mapped_port}" \
DOMUS_TEST_S3_ACCESS_KEY='domus-test-access' \
DOMUS_TEST_S3_SECRET_KEY='domus-test-secret' \
GOWORK=off \
go test ./internal/store -run '^TestSeaweedFSPrefixIsolation$' -count=1 -v
