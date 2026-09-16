# Security notes

## What needs protection

- Documents, previews, and search data
- Search text and results
- Access rules
- Login tokens and signing keys
- Saved indexes, logs, and backups

## What the services can see

Each search node can see the collection it owns and the searches sent to it. The coordinator can see
a search while it checks the user, sends the request, and joins the results. It does not keep a main
document store.

The project expects that someone may send bad requests, reuse a stolen token, pretend to be a node,
watch an unprotected connection, stop a service, or read logs and metrics.

## Built-in protection

- Request size limits and timeouts
- Login token checks at the coordinator
- Short-lived signed tokens between services
- Access checks on the node that owns the document
- Extra permission checks for writes
- Checks for damaged or oversized saved data
- Safe handling of repeated write requests
- Scrambled user and resource IDs in audit logs
- Metrics and traces that leave out private content
- Locked-down Kubernetes containers
- Clear warnings when part of a search could not run

## Before using it in production

- Use HTTPS at the public entry point.
- Encrypt and check traffic between services.
- Store keys in a real secret manager and rotate them.
- Encrypt storage and backups.
- Limit network access to the services and monitoring pages.
- Turn off demo mode and connect a trusted login provider.

The app does not set up encrypted service-to-service traffic by itself. The cluster or a service mesh
must provide it.

## Limits

PrivateMesh does not hide a query from the coordinator or the nodes asked to search it. It also
cannot stop an allowed user from copying a document they can open. A node owner can leave out or
reorder results from their own collection.
