# Security policy

## Supported versions

Security fixes are provided for the latest tagged release.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting for this repository instead of opening a public issue. Include affected versions, reproduction steps, impact, and any suggested mitigation. Do not include live credentials, personal data, or third-party systems in a report.

Maintainers will acknowledge a report within seven days and coordinate disclosure after a fix is available. There is currently no bug bounty.

## Deployment responsibilities

Turnip terminates plain HTTP/WebSocket connections. Internet deployments must put it behind TLS, redact the `token` query parameter from access logs, restrict Redis to a private network, use short-lived room-scoped JWTs, and protect the metrics listener.
