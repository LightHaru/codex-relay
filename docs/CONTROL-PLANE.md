# Relay Control Plane

The Relay exposes a small, versioned control plane alongside the existing
management API. It follows a Cockpit-like split between process liveness,
pool readiness, and operational diagnostics while keeping the native Codex
task/data plane unchanged.

The current contract identifier is `relay.control.v1`. Every JSON control
response includes `schemaVersion: "relay.control.v1"`; clients must use that
field before interpreting fields that may be added in a future release.

## Authentication

All control endpoints except `GET /v1/control/healthz` require the local Relay
control token. Prefer the request header:

```text
X-Codex-Mux-Token: <local-control-token>
```

The legacy `?token=` query parameter remains accepted for compatibility, but
should not be used in scripts or URLs that may be logged. The API is intended
for the local packaged renderer and local diagnostics; it is not a public
network API.

## Lifecycle endpoints

### `GET /v1/control/healthz` — process liveness

This endpoint is unauthenticated and only answers whether the HTTP control
server is alive. It does not probe credentials, quota, worker children, or
upstream availability. A live response is HTTP `200`:

```json
{
  "schemaVersion": "relay.control.v1",
  "status": "live",
  "ok": true,
  "generatedAt": 1767000000000,
  "checks": {"process": {"status": "ok"}}
}
```

### `GET /v1/control/readyz` — pool readiness

This endpoint is token-protected. It evaluates the current aggregate pool and
returns a status suitable for a readiness probe:

| Status | HTTP | Meaning |
| --- | --- | --- |
| `ready` | `200` | Pool health is `healthy` and at least one subscription is available for routing. |
| `degraded` | `503` | The process is alive, but the pool is depleted, partially connected, or still has unknown quota evidence. |
| `not_ready` | `503` | No usable pool has been observed yet (for example, a fresh `warming` launch). |

The response contains only aggregate pool/session fields. A failed internal
status read returns `not_ready` with reason `router_unavailable`; it never
returns an upstream body or credential error.

## Pool and session snapshots

### `GET /v1/control/pool`

Returns the aggregate pool projection:

```json
{
  "schemaVersion": "relay.control.v1",
  "generatedAt": 1767000000000,
  "pool": {
    "poolId": "codex-relay-pool",
    "revision": 12,
    "health": "healthy",
    "activeLeaseCount": 0,
    "connectedSubscriptions": 5,
    "knownSubscriptions": 5,
    "unknownSubscriptions": 0,
    "availableSubscriptions": 5,
    "depletedSubscriptions": 0,
    "maximumPercent": 500,
    "confirmedRemainingPercent": 377,
    "confirmedUsedPercent": 123,
    "nextResetAt": 1767003600000,
    "quotaUpdatedAt": 1767000000000
  }
}
```

`poolId` is the stable public pool identity. The response does not include
source IDs, labels, email addresses, filesystem paths, access/refresh tokens,
raw account records, or raw upstream payloads.

### `GET /v1/control/snapshot`

Returns pool and task-session state in one read. It also includes up to the 50
most recent sanitized diagnostics events:

```json
{
  "schemaVersion": "relay.control.v1",
  "generatedAt": 1767000000000,
  "pool": {"health": "healthy", "availableSubscriptions": 5},
  "session": {"activeTurnCount": 1, "recoveryTaskCount": 0},
  "diagnostics": {
    "eventCount": 2,
    "events": [
      {"type": "turn.started", "timestamp": 1766999999000, "routeGeneration": 3},
      {"type": "response.completed", "timestamp": 1767000000000, "routeGeneration": 3, "result": "completed"}
    ]
  }
}
```

## Diagnostics and events

### `GET /v1/control/diagnostics?limit=N`

Returns the newest diagnostics events from the bounded router timeline. `N`
defaults to 50 and must be between 1 and 200. Ordering is chronological; when
the timeline is larger than `N`, only its newest `N` entries are returned.

Each event is projected to lifecycle-safe fields: `type`, `timestamp`,
`routeGeneration`, `reasonCode`, and `result`. Account IDs, email/username,
labels, arbitrary reasons, command text, authorization headers, paths, and
upstream response data are intentionally omitted.

### `GET /v1/control/events`

Opens a token-protected Server-Sent Events stream. The first frame is a
comment (`: connected`). Subsequent `data:` frames preserve arrival order and
contain the same sanitized event projection as diagnostics. Disconnecting the
client closes only this subscriber; it does not affect task streams or pool
workers.

## Safe local examples

The following examples use a placeholder token and loopback address. Replace
`REPLACE_WITH_LOCAL_TOKEN` with the token supplied by the local Relay process;
do not paste a real access or refresh token into a shell history or issue.

```powershell
$headers = @{ "X-Codex-Mux-Token" = "REPLACE_WITH_LOCAL_TOKEN" }

# Liveness does not require a token.
Invoke-RestMethod http://127.0.0.1:48123/v1/control/healthz

# Readiness and aggregate pool status.
Invoke-RestMethod http://127.0.0.1:48123/v1/control/readyz -Headers $headers
Invoke-RestMethod http://127.0.0.1:48123/v1/control/pool -Headers $headers

# Bounded diagnostics.
Invoke-RestMethod "http://127.0.0.1:48123/v1/control/diagnostics?limit=25" -Headers $headers
```

For SSE diagnostics, use a client that keeps the response body open (for
example `curl --no-buffer`) and stop it with `Ctrl+C`:

```text
curl --no-buffer -H "X-Codex-Mux-Token: REPLACE_WITH_LOCAL_TOKEN" \
  http://127.0.0.1:48123/v1/control/events
```

The control plane is observational. Account login/removal, routing policy,
and native Codex task operations continue to use their existing token-protected
routes; control snapshots never mutate pool state.
