# Operator runbook

## Local environment

Start the full topology in the background:

```bash
docker compose -f deployments/compose/compose.yaml up -d --build
docker compose -f deployments/compose/compose.yaml ps
```

The coordinator and search nodes expose `/healthz`, `/readyz`, `/version`, and `/metrics`. The
expected steady state is one coordinator, two healthy search nodes, one web container, and four
observability containers. Prometheus should report three healthy application targets. Jaeger should
list `coordinator` and `search-node` after the first query.

Use `make down` to stop containers while retaining node, Prometheus, and Grafana volumes. Removing
named volumes deletes local indexed data and telemetry history.

## Kubernetes demonstration

The Kubernetes image names track the v1.0.0 release published by the tag workflow.

```bash
make kubernetes-config
kubectl apply -k deployments/kubernetes/overlays/demo
kubectl -n privatemesh rollout status deployment/coordinator
kubectl -n privatemesh rollout status statefulset/search-node-a
kubectl -n privatemesh rollout status statefulset/search-node-b
kubectl -n privatemesh port-forward service/web 18000:8080
```

This overlay contains deterministic demonstration credentials and seeded documents. Do not expose
it to an untrusted network.

## Production configuration

Before applying the production overlay:

1. Replace the example issuer and client ID in
   `deployments/kubernetes/overlays/production/runtime.yaml`.
2. Generate fresh principal and audit keys directly into the cluster:

   ```bash
   make deployment-keys | kubectl apply -f -
   ```

3. Provide encrypted ingress and authenticated service-to-service encryption. A service mesh is the
   intended place to enforce gRPC mTLS for this release.
4. Encrypt the storage class used by both StatefulSets and configure volume snapshots.
5. Apply namespace ingress/egress policy for the web-to-coordinator, coordinator-to-node, DNS,
   identity-provider, metrics, and telemetry paths used by your cluster.
6. Confirm the OIDC access tokens contain `search.execute` and `documents.read`; mutation clients
   also require `documents.write`.

Then render and apply:

```bash
kubectl kustomize deployments/kubernetes/overlays/production >/dev/null
kubectl apply -k deployments/kubernetes/overlays/production
```

The browser bundle demonstrates the unauthenticated local flow. Production API clients must attach
their OIDC token as `Authorization: Bearer <token>` unless the deployment adds its own OIDC-aware
proxy in front of the web service.

## Healthy-state checks

```bash
curl --fail http://127.0.0.1:18080/readyz
curl --fail http://127.0.0.1:18080/api/nodes
curl --fail http://127.0.0.1:19090/-/ready
curl --fail http://127.0.0.1:13000/api/health
```

For Kubernetes, use an equivalent port-forward or run the requests from an authorized diagnostic
pod. A healthy registry lists every expected node and its collection. An increasing applied log
offset after writes confirms heartbeats are updating the coordinator.

## Missing-shard incident

1. Check `unavailable_shard_ids` in the search response and the unavailable-shard panel in Grafana.
2. Match the shard prefix to the node ID returned by `/api/nodes`.
3. Inspect the owning pod's readiness, restart count, last termination reason, and persistent-volume
   attachment.
4. Check coordinator registration warnings and node heartbeat warnings. These logs contain no query
   or document content.
5. Restore the node. It should replay its WAL, register a new lease, and disappear from
   `unavailable_shard_ids` without a coordinator restart.

The automated local version of this procedure is `make chaos-test`.

## Recovery and backup

The committed WAL is the authoritative runtime recovery source. Take crash-consistent snapshots of
the node PVCs and retain the entire collection directory. Do not copy only the WAL or only segment
files.

To restore a node, stop its StatefulSet, attach a restored volume at `/var/lib/privatemesh`, and
start the pod with the same collection ID. Readiness must not be considered sufficient until a
known document can be read and a search response includes the node's shard ID.

Run `make recovery-test` before releases or storage changes. It writes a unique document, restarts
the research node, verifies the exact content after replay, and deletes the test document.

## Capacity and load

Use `make load-test` against a running topology. The command fails when its default 1% error budget
or 500 ms p95 latency budget is exceeded. Override request count, concurrency, endpoint, or command
flags for the expected deployment load.

Use `make benchmark-million` to measure the local search core independently of network and policy
cost. Compare its JSON output by seed, Go version, document count, heap, throughput, and latency;
do not compare only wall-clock time.

Search node memory grows with indexed content. Increase memory requests and limits based on measured
corpus shape before increasing collection size. Add a new uniquely identified node and collection
rather than increasing a StatefulSet replica count, because replicas would otherwise share a node
identity and registration record.

## Upgrade and rollback

1. Back up node PVCs and run the recovery check.
2. Upgrade one search-node StatefulSet and verify WAL replay, registration, reads, and searches.
3. Upgrade the second node and repeat.
4. Upgrade the coordinator, then the stateless web deployment.
5. Confirm Prometheus targets, error rate, unavailable shards, and traces.

Roll back to the previous immutable image tag if health or correctness checks fail. Preserve the
PVCs; never replace them as part of an application rollback. Segment and WAL readers reject unknown
formats instead of silently accepting incompatible data.
