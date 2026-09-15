#!/usr/bin/env bash

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${PRIVATEMESH_COMPOSE_FILE:-${root_dir}/deployments/compose/compose.yaml}"
coordinator_url="${PRIVATEMESH_COORDINATOR_URL:-http://127.0.0.1:18080}"
node_url="${PRIVATEMESH_NODE_B_URL:-http://127.0.0.1:18092}"
document_id="recovery-check-$(date +%s)"
content="Durable recovery verification ${document_id}"

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

delete_document() {
  curl --silent --output /dev/null --request DELETE \
    "${coordinator_url}/api/documents/research/${document_id}" || true
}

trap delete_document EXIT
wait_for_url "${coordinator_url}/readyz"
wait_for_url "${node_url}/readyz"
curl --fail --silent --show-error \
  -H "Content-Type: application/json" \
  --data "{\"collection_id\":\"research\",\"document_id\":\"${document_id}\",\"title\":\"Recovery check\",\"content\":\"${content}\"}" \
  "${coordinator_url}/api/documents" >/dev/null

docker compose -f "${compose_file}" restart search-node-b >/dev/null
wait_for_url "${node_url}/readyz"

for _ in {1..30}; do
  recovered="$(curl --silent --show-error "${coordinator_url}/api/documents/research/${document_id}")"
  if grep --quiet "${content}" <<<"${recovered}"; then
    echo "Recovery test passed: the document survived a process restart."
    exit 0
  fi
  sleep 1
done
echo "The document was not recovered after restart." >&2
exit 1
