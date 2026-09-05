# Contributing

Turnip accepts focused issues and pull requests. Protocol or security changes should start with an issue describing compatibility and threat-model implications.

Before submitting a change:

```sh
make format
make test
make test-race
make lint
make vuln
```

Run `make test-integration` with `TURNIP_TEST_REDIS_URL` pointing to a disposable Redis database when changing room state, Pub/Sub, WebSockets, or shutdown behavior. Do not run the integration suite against shared or production Redis because it flushes the selected database.

Commits must not contain tokens, secrets, `.env` files, signal payloads from real users, or generated binaries. Update README protocol documentation and `CHANGELOG.md` for user-visible behavior.
