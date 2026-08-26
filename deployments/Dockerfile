# ---- Tailwind CSS ----
FROM node:22-alpine AS tailwind
WORKDIR /app
COPY cmd/parser/web/package.json cmd/parser/web/package-lock.json ./
RUN npm ci
COPY cmd/parser/web/ cmd/parser/web/
RUN for f in cmd/parser/web/templates/*.templ; do \
      [ -f "$f" ] && cp "$f" "${f}.html"; \
    done && \
    npx @tailwindcss/cli -i cmd/parser/web/static/input.css -o app.css --minify

# ---- Templ ----
FROM golang:1.27-alpine AS templ
ARG TEMPL_VERSION=v0.3.1020
RUN go install github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/parser/web/templates/ cmd/parser/web/templates/
RUN templ generate -path ./cmd/parser/web/templates/

# ---- Build ----
FROM golang:1.27-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=tailwind /app/app.css cmd/parser/web/static/app.css
COPY --from=templ /app/cmd/parser/web/templates/*_templ.go cmd/parser/web/templates/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/idolhub ./cmd/parser

# ---- Runtime ----
FROM alpine:3.21

ARG YT_DLP_VERSION=2026.08.19

RUN apk add --no-cache \
    ffmpeg \
    python3 \
    py3-pip \
    deno \
    ca-certificates \
    tzdata \
    curl \
    su-exec \
    && pip install --no-cache-dir --break-system-packages curl_cffi \
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
