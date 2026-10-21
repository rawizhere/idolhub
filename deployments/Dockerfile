# syntax=docker/dockerfile:1

# ---- Build ----
# app.css is the committed build; CI checks that it matches a fresh Tailwind run.
FROM golang:1.27-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go tool templ generate -path ./cmd/parser/web/templates/ && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/idolhub ./cmd/parser

# ---- Runtime ----
FROM alpine:3.24

ARG YT_DLP_VERSION=2026.08.19

RUN apk upgrade --no-cache \
    && apk add --no-cache \
    ffmpeg \
    python3 \
    py3-pip \
    deno \
    ca-certificates \
    tzdata \
    curl \
    su-exec \
    && pip install --break-system-packages curl_cffi \
    && curl -fsSL https://github.com/yt-dlp/yt-dlp/releases/download/${YT_DLP_VERSION}/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp \
    && rm -rf /var/cache/apk/*

RUN adduser -D -h /home/idolhub idolhub \
    && chown -R idolhub:idolhub /usr/local/bin

WORKDIR /app

COPY --from=builder /app/idolhub .
COPY deployments/entrypoint.sh /app/entrypoint.sh

RUN chown -R idolhub:idolhub /app && chmod +x /app/entrypoint.sh

VOLUME ["/app/downloads"]

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl -f http://localhost:8080/api/config || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["./idolhub"]
