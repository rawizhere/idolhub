.PHONY: all build run lint templ css clean docker-build

GO ?= go
BIN ?= idolhub

all: build

templ:
	$(GO) tool templ generate -path ./cmd/parser/web/templates/

css:
	npm ci --silent
	npx @tailwindcss/cli -i cmd/parser/web/static/input.css -o cmd/parser/web/static/app.css --minify

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
	docker build -f deployments/Dockerfile -t idolhub .

docker-run:
	docker compose -f deployments/docker-compose.dev.yml up -d
