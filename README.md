# Turnip

Turnip is a small, authenticated WebRTC signaling relay for applications that need multi-instance room presence without running media through the server. It validates room-scoped JWTs, tracks short-lived connection leases in Redis, and relays ephemeral JSON signals to a specific participant.

Turnip does **not** provide STUN/TURN, media transport, message persistence, or guaranteed delivery.

## Quick start

Requirements: Docker with Compose, or Go 1.25.13+ and Redis 7.4+.

```sh
cp .env.example .env
docker compose up -d --build
make token SUBJECT=alice ROOM=demo NAME=Alice
make token SUBJECT=bob ROOM=demo NAME=Bob
```

Open [http://localhost:8080](http://localhost:8080) in two tabs. Put one token in each tab and connect one tab to `ws://localhost:5004/v1/ws` and the other to `ws://localhost:5005/v1/ws`. The two server instances share Redis but do not require sticky sessions.

The Compose demo exposes application ports 5004 and 5005, metrics on loopback ports 9090 and 9091, and the development client on 8080. Redis is not exposed to the host.

To run the binary directly:

```sh
cp .env.example .env
docker run --rm -p 127.0.0.1:6379:6379 redis:7.4-alpine
make build
./bin/turnip
```

The Makefile loads `.env` when it exists.

## Authentication

`GET /v1/ws` requires an external HS256 JWT. Native clients may send `Authorization: Bearer <JWT>`; browser clients may use `?token=<JWT>`. If both are present they must match.

Required claims:

| Claim | Meaning |
| --- | --- |
| `sub` | Stable user subject. Room ownership is tied to this value. |
| `iss` | Must equal `TURNIP_JWT_ISSUER`. |
| `aud` | Must contain `TURNIP_JWT_AUDIENCE`. |
| `exp` | Required expiration time. A 30-second clock skew is allowed. |
| `room_id` | Authorized room, matching `[A-Za-z0-9_-]{1,128}`. |
| `name` | Optional display name, at most 80 Unicode characters. |

`nbf` is honored when present. Only HS256 is accepted. The development token command creates 15-minute tokens by default:

```sh
make token SUBJECT=alice ROOM=demo NAME=Alice
go run ./cmd/turnip-token -subject alice -room demo -name Alice -lifetime 10m
```

Query-string credentials can appear in proxy access logs. Internet deployments must use WSS, short token lifetimes, and query redaction. Turnip never logs tokens or signaling payloads.

## Protocol v1

Authentication joins the authorized room immediately. The server assigns a new participant ID to every WebSocket connection. A subject may have multiple simultaneous connections.

Every client message needs an `id` containing 1–64 letters, digits, `.`, `_`, `:`, or `-`.

Targeted signal:

```json
{
  "id": "offer-1",
  "type": "signal",
  "data": {
    "to": "target-participant-id",
    "payload": { "kind": "offer", "sdp": "opaque application data" }
  }
}
```

Owner disband request:

```json
{ "id": "close-1", "type": "disband_room", "data": {} }
```

Server event types:

- `ready`: room ID, caller participant ID, owner flag, and active participant snapshot.
- `participant_joined` / `participant_left`: participant ID, optional name, and owner flag.
- `signal`: server-derived sender participant ID, request ID, and opaque payload.
- `room_disbanded`: reason and initiating owner participant ID, followed by WebSocket close code `4001`.
- `error`: stable code, safe message, and related request ID when available.

Signals are delivered at most once through Redis Pub/Sub. A target that has disconnected or whose 90-second lease expired returns `participant_not_found`. Messages are limited to 64 KiB, rooms to 32 live connections, and each connection to 50 messages per second with a burst of 100.

The first subject to join a missing room becomes its owner. If that owner disconnects while members remain, ownership stays with the subject and is restored on reconnect. The room disappears when its final lease is removed. Only the owner can manually disband it.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `TURNIP_HTTP_ADDR` | `:5004` | Main HTTP listen address. |
| `TURNIP_REDIS_URL` | `redis://localhost:6379/0` | Redis URL; `rediss://` is supported. |
| `TURNIP_INSTANCE_ID` | hostname | Unique server instance ID, 1–128 characters. |
| `TURNIP_JWT_SECRET` | required | HS256 secret, at least 32 bytes. |
| `TURNIP_JWT_ISSUER` | required | Required JWT issuer. |
| `TURNIP_JWT_AUDIENCE` | required | Required JWT audience. |
| `TURNIP_ALLOWED_ORIGINS` | empty | Comma-separated browser origins. Requests without `Origin` are allowed. `*` is an explicit development-only wildcard. |
| `TURNIP_METRICS_ADDR` | `127.0.0.1:9090` | Metrics listen address; empty disables metrics. |
| `TURNIP_METRICS_USERNAME` | empty | Optional Basic Auth username; must be paired with password. |
| `TURNIP_METRICS_PASSWORD` | empty | Optional Basic Auth password; must be paired with username. |
| `TURNIP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `TURNIP_LOG_FORMAT` | `console` | `console` or `json`. |
| `TURNIP_VERSION` | `dev` | Runtime version when not injected at build time. |

Invalid configuration fails before the server starts. Browser origins must be explicitly allowed; native clients without an `Origin` header are accepted.

## Operations

- `GET /health/live` reports process liveness.
- `GET /health/ready` requires an initialized Redis subscription and a successful Redis ping.
- `GET /metrics` is served on the separate metrics address and supports optional Basic Auth.

Prometheus labels are bounded and contain no user, room, token, or payload values. Metrics cover authentication outcomes, local connection counts, messages, room lifecycle observations, dropped messages, Redis latency, readiness failures, and standard Go/process collectors.

Presence is a Redis lease renewed every 30 seconds and valid for 90 seconds. Instances sweep stale leases, while key expiry provides a backstop. Redis Pub/Sub is intentionally ephemeral: applications must renegotiate after reconnecting.

On SIGINT or SIGTERM, Turnip stops HTTP acceptance, closes WebSockets, unregisters presence, stops workers, shuts down metrics, and closes Redis within ten seconds.

## Development

```sh
make deps
make format
make test
make test-race
make lint
make vuln
```

Redis-backed integration tests require a disposable Redis database:

```sh
docker run --rm -d --name turnip-test-redis -p 127.0.0.1:16379:6379 redis:7.4-alpine
TURNIP_TEST_REDIS_URL=redis://localhost:16379/15 make test-integration
docker stop turnip-test-redis
```

CI runs formatting, vet, lint, race tests, Redis integration tests, vulnerability scanning, and a multi-platform container build. Release tags publish `linux/amd64` and `linux/arm64` images to `ghcr.io/liberocks/turnip` with SBOM and provenance metadata.

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution checks and [SECURITY.md](SECURITY.md) for private vulnerability reporting.

## License

MIT. See [LICENSE](LICENSE).
