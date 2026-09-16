# Operations

## Local setup

Start everything:

```bash
docker compose -f deployments/compose/compose.yaml up -d --build
docker compose -f deployments/compose/compose.yaml ps
```

The coordinator and search nodes have `/healthz`, `/readyz`, `/version`, and `/metrics` pages. Once
you run a search, Jaeger should show `coordinator` and `search-node`.

Run `make down` to stop the containers without deleting saved data. Deleting the Docker volumes also
deletes local documents and monitoring history.

## Kubernetes demo

The demo files use the version 1.0.0 container images.

```bash
make kubernetes-config
kubectl apply -k deployments/kubernetes/overlays/demo
kubectl -n privatemesh rollout status deployment/coordinator
kubectl -n privatemesh rollout status statefulset/search-node-a
kubectl -n privatemesh rollout status statefulset/search-node-b
kubectl -n privatemesh port-forward service/web 18000:8080
```

The demo includes public example keys and sample documents. Do not expose it to an untrusted
network.

## Production setup

Before using the production files:

1. Set your login provider and client ID in
   `deployments/kubernetes/overlays/production/runtime.yaml`.
2. Create new signing and audit keys:

   ```bash
   make deployment-keys | kubectl apply -f -
   ```

3. Add HTTPS and encrypted traffic between services.
4. Use encrypted storage and set up volume snapshots.
5. Limit network access to only the services that need to talk to each other.
6. Make sure login tokens include `search.execute` and `documents.read`. Apps that write documents
   also need `documents.write`.

Check and apply the files:

```bash
kubectl kustomize deployments/kubernetes/overlays/production >/dev/null
kubectl apply -k deployments/kubernetes/overlays/production
```

The included browser app is set up for the local demo. Production API clients must send a login
token as `Authorization: Bearer <token>` unless another login service handles that first.

## Health checks

```bash
curl --fail http://127.0.0.1:18080/readyz
curl --fail http://127.0.0.1:18080/api/nodes
curl --fail http://127.0.0.1:19090/-/ready
curl --fail http://127.0.0.1:13000/api/health
```

In Kubernetes, port-forward these services or run the checks from a trusted pod. `/api/nodes`
should list every expected node and collection.

## When a collection is missing from results

1. Check `unavailable_shard_ids` in the response or the missing-shard panel in Grafana.
2. Find the matching node in `/api/nodes`.
3. Check that node's readiness, restarts, last error, and storage connection.
4. Check coordinator and node warnings.
5. Restore the node. It should reload its saved data and rejoin without restarting the coordinator.

Run `make chaos-test` to test this locally.

## Backups and recovery

Snapshot the full storage volume for each node. Keep the whole collection directory together; do
not back up only one of its files.

To restore a node, stop it, attach the restored volume at `/var/lib/privatemesh`, and start it with
the same collection ID. Confirm that a known document can be opened and found in search.

Run `make recovery-test` before releases or storage changes. It writes a test document, restarts a
node, checks the document, and removes it.

## Load and size checks

Run `make load-test` against a running setup. It fails if too many requests fail or take too long.
You can change the request count, number of workers, and URL with command options.

Run `make benchmark-million` to measure local indexing and search. Keep the seed, Go version,
document count, memory use, speed, and timing when saving results.

Measure real document data before raising node memory limits or collection sizes. Give every new
node its own ID and collection setting.

## Upgrades and rollback

1. Back up node storage and run the recovery check.
2. Upgrade one search node and check restart recovery, registration, reads, and searches.
3. Upgrade the next search node and repeat.
4. Upgrade the coordinator and then the web app.
5. Check targets, errors, missing collections, and traces.

If checks fail, return to the last working image tag. Keep the saved volumes in place during a
rollback.
