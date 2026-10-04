# ferry 统一入口：make build / make test / make dev
.PHONY: build build-server build-agent test dev fmt

build: build-server

build-server:
	cd server && CGO_ENABLED=0 go build -o ../bin/ferry-server ./cmd/ferry

build-agent:
	cd agent && CGO_ENABLED=0 go build -o ../bin/ferry-agent ./cmd/agent

test:
	cd server && go test ./...
	cd packages/agentproto && go test ./...

dev:
	cd server && go run ./cmd/ferry

fmt:
	gofmt -w server packages/agentproto
