# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/local-web-eyes ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata chromium && \
    addgroup -S app && adduser -S -G app app && \
    mkdir -p /app/data && chown -R app:app /app

WORKDIR /app
COPY --from=builder /out/local-web-eyes /usr/local/bin/local-web-eyes
COPY configs/config.example.yaml /app/config.yaml

USER app
ENV LWE_TRANSPORT=http \
    LWE_HTTP_ADDR=0.0.0.0:8090 \
    LWE_HTTP_PATH=/mcp \
    LWE_DB_PATH=/app/data/web_eyes.db \
    LWE_BROWSER_BIN=/usr/bin/chromium-browser

EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8090/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/local-web-eyes", "--config", "/app/config.yaml"]
