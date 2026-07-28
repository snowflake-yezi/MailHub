# NR-P7 Canary and Legacy Rollback

Status: code complete (remote canary and firewall acceptance pending)

## Scope

NR-P7 moves transport selection from an implicit database field to an explicit,
audited canary operation. A node may enter `dual` or `control_stream` only after
its enrolled identity has an active control lease and reports `ready`.

## Implemented in this slice

- `POST /api/v1/admin/servers/:id/transport` changes one node between
  `legacy_http`, `dual`, and `control_stream`.
- The transition and its `transport.mode.change` audit record are committed in
  one database transaction.
- `legacy_http_enabled: false` blocks every legacy Execute, Notify, Query,
  OpenData, and Probe path, including migration fallback.
- `dual` is rejected when legacy fallback is disabled; `control_stream` remains
  available for the final migrated state.
- `GET /api/v1/admin/servers/transport-preflight` reports fleet cutover blockers.
- `dual` query reads run a bounded legacy shadow read and compare status/body hashes without changing the primary result.
- An administrator can migrate a selected legacy server in place; approval binds
  the node UUID to the existing server ID without moving mailbox/domain ownership.
- A dual node's local HTTP routes accept either its node credential or the
  retained shared secret, so legacy probes, shadow reads, and fallback remain usable.
- Control-stream nodes use their node credential for management HTTP calls and do not bind the local legacy HTTP listener.

## Remote acceptance still required

- Run the endpoint one node at a time with a recorded canary and rollback
  result; no automatic fleet-wide switch is provided.
- Verify all business paths after switching to `control_stream`, including
  attachment/raw EML reads and lifecycle jobs.
- Apply the network policy that blocks management-to-node `8081` only after all
  nodes are on `control_stream` and rollback evidence is retained.
- Remove shared-secret configuration from migrated nodes only after the
  firewall and rollback drills pass.

## Rollback rule

Rollback is an explicit transition to `dual` or `legacy_http`. It never rewinds
node identity, credentials, leases, or durable command results. `legacy_http`
rollback is rejected while the global legacy transport gate is disabled.
