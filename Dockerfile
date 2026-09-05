# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25.13
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILT_AT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.builtAt=$BUILT_AT" \
    -o /out/turnip ./cmd/turnip

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S turnip && \
    adduser -u 10001 -S turnip -G turnip
WORKDIR /app
COPY --from=builder /out/turnip /app/turnip
USER 10001:10001
EXPOSE 5004 9090
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:5004/health/live || exit 1
ENTRYPOINT ["/app/turnip"]
