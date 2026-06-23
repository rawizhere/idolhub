.PHONY: all build run lint templ css clean docker-build

GO ?= go
BIN ?= idolhub

all: build

templ:
	templ generate -path ./cmd/parser/web/templates/

css:
	rm -rf node_modules package.json package-lock.json
	npm init -y > /dev/null
	npm install -q tailwindcss @tailwindcss/cli
	npx @tailwindcss/cli -i cmd/parser/web/static/input.css -o cmd/parser/web/static/app.css --minify
	rm -rf node_modules package.json package-lock.json

build: templ css
	CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -o $(BIN) ./cmd/parser

run: build
	./$(BIN)

lint:
	golangci-lint run --timeout=5m --config=.golangci.yml ./...

clean:
	rm -f $(BIN)
	rm -f cmd/parser/web/templates/*_templ.go
	rm -f cmd/parser/web/static/app.css

docker-build:
	docker build -f deploy/Dockerfile -t idolhub .

docker-run:
	docker compose -f docker-compose.dev.yml up -d
