# ---- Tailwind CSS ----
FROM node:22-alpine AS tailwind
WORKDIR /app
RUN npm init -y && npm install tailwindcss @tailwindcss/cli
COPY cmd/parser/web/ cmd/parser/web/
RUN for f in cmd/parser/web/templates/*.templ; do \
      [ -f "$f" ] && cp "$f" "${f}.html"; \
    done && \
    npx @tailwindcss/cli -i cmd/parser/web/static/input.css -o app.css --minify

# ---- Templ ----
FROM golang:1.26-alpine AS templ
RUN go install github.com/a-h/templ/cmd/templ@latest
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/parser/web/templates/ cmd/parser/web/templates/
RUN templ generate -path ./cmd/parser/web/templates/

# ---- Build ----
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=tailwind /app/app.css cmd/parser/web/static/app.css
COPY --from=templ /app/cmd/parser/web/templates/*_templ.go cmd/parser/web/templates/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/idolhub ./cmd/parser

# ---- Runtime ----
FROM alpine:3.20

RUN apk add --no-cache \
    chromium \
    ffmpeg \
    ca-certificates \
    tzdata \
    curl \
    su-exec \
    && rm -rf /var/cache/apk/*

RUN adduser -D -h /home/idolhub idolhub

WORKDIR /app

COPY --from=builder /app/idolhub .
COPY deploy/entrypoint.sh /app/entrypoint.sh

RUN chown -R idolhub:idolhub /app && chmod +x /app/entrypoint.sh

ENV CHROME_BIN=/usr/bin/chromium-browser \
    CHROME_PATH=/usr/bin/chromium-browser

VOLUME ["/app/downloads"]

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD curl -f http://localhost:8080/api/config || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["./idolhub"]
