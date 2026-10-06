# ferry 统一入口：make build / make test / make dev
.PHONY: build build-server build-agent test test-pg dev fmt

build: build-server

build-server:
	cd server && CGO_ENABLED=0 go build -o ../bin/ferry-server ./cmd/ferry

build-agent:
	cd agent && CGO_ENABLED=0 go build -o ../bin/ferry-agent ./cmd/agent

test:
	cd server && go test ./...
	cd agent && go test ./...
	cd packages/agentproto && go test ./...

# 需本机 docker：起临时 PostgreSQL 跑存储三方言集成测试
test-pg:
	docker start ferry-pg-test 2>/dev/null || docker run -d --name ferry-pg-test -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ferry -p 54329:5432 postgres:15-alpine
	@sleep 3
	cd server && FERRY_TEST_PG_DSN="host=127.0.0.1 port=54329 user=postgres password=postgres dbname=ferry sslmode=disable" go test -count=1 ./internal/storage/

dev:
	cd server && go run ./cmd/ferry

fmt:
	gofmt -w server agent packages/agentproto
