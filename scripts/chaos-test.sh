#!/usr/bin/env bash

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${PRIVATEMESH_COMPOSE_FILE:-${root_dir}/deployments/compose/compose.yaml}"
coordinator_url="${PRIVATEMESH_COORDINATOR_URL:-http://127.0.0.1:18080}"
node_url="${PRIVATEMESH_NODE_B_URL:-http://127.0.0.1:18092}"
target_service="${PRIVATEMESH_CHAOS_TARGET:-search-node-b}"

search() {
  curl --fail --silent --show-error \
    -H "Content-Type: application/json" \
    --data '{"query":"distributed search","mode":"auto","limit":10}' \
    "${coordinator_url}/api/search"
}

wait_for_url() {
  local url="$1"
  for _ in {1..60}; do
    if curl --fail --silent --output /dev/null "${url}"; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for ${url}" >&2
  return 1
}

restore_node() {
  docker compose -f "${compose_file}" up -d "${target_service}" >/dev/null
  wait_for_url "${node_url}/readyz"
}

trap restore_node EXIT
wait_for_url "${coordinator_url}/readyz"
wait_for_url "${node_url}/readyz"
search | grep --quiet '"unavailable_shard_ids":\[\]'

docker compose -f "${compose_file}" kill "${target_service}" >/dev/null
for _ in {1..30}; do
  response="$(search)"
  if grep --quiet 'node-b:research' <<<"${response}"; then
    break
  fi
  sleep 1
done
grep --quiet '"unavailable_shard_ids":\["node-b:research"\]' <<<"${response}"

restore_node
trap - EXIT
for _ in {1..30}; do
  response="$(search)"
  if grep --quiet '"unavailable_shard_ids":\[\]' <<<"${response}"; then
    echo "Chaos test passed: failure was explicit and the node rejoined."
    exit 0
  fi
  sleep 1
done
echo "The recovered node did not rejoin the search topology." >&2
exit 1
