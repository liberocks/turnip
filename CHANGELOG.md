# Changelog

All notable changes are documented here. Turnip follows semantic versioning after the v1 wire protocol introduced in `0.1.0`.

## [Unreleased]

## [0.1.0] - 2026-09-05

- Introduced the authenticated `/v1/ws` targeted signaling protocol.
- Added room-scoped external HS256 JWT validation and creator ownership.
- Added atomic Redis room state, per-connection presence leases, and cross-instance disbandment.
- Added origin checks, message limits, rate limiting, WebSocket keepalives, and graceful shutdown.
- Added bounded Prometheus metrics, liveness/readiness probes, multi-architecture containers, tests, CI, and release automation.
