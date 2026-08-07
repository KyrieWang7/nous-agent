.PHONY: check test race lint fmt fmt-check tidy-check coverage layer-check build migrate

# 默认门禁：离线、无网络、无 API key，与 CI 一致
check: fmt-check tidy-check lint layer-check test

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy-check:
	go mod tidy -diff

coverage:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

layer-check:
	go run ./internal/tools/layercheck

build:
	go build ./...

migrate:
	go run ./cmd/agentctl migrate up
