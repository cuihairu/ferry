# ferry 统一入口：make build / make test / make dev
.PHONY: build build-server build-agent build-quic package-agent build-dash build-panel test test-pg dev dev-server dev-dash dev-panel fmt

# AGENT_VERSION 是发布包版本号（package-agent 用，与 deploy/agent-install.sh 的 --version 对应）。
AGENT_VERSION ?= dev
GOARCH ?= $(shell go env GOARCH)

build: build-server build-agent build-quic build-dash build-panel

build-server:
	cd server && CGO_ENABLED=0 go build -o ../bin/ferry-server ./cmd/ferry

build-agent:
	cd agent && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../bin/ferry-agent ./cmd/agent
	$(MAKE) check-agent-size

# E-18b：QUIC 传输 sidecar（quic-go 只落此二进制，agent 主程序不携带）。
# 可选发布件：不用 QUIC 传输的部署不装它，agent 主程序尺寸门禁（E-4）不量它。
build-quic:
	cd agent && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../bin/ferry-quic ./cmd/ferry-quic

# E-4 门禁：核心静态二进制 ≤10MB（核心最小集口径见 agent/internal/roles）。
check-agent-size:
	@test $$(stat -c%s bin/ferry-agent) -le 10485760 || (echo "ferry-agent exceeds 10MB core budget" && exit 1)

# A-24：打 agent 发布包（bin/ 下 tar.gz + .sha256，与 deploy/agent-install.sh 下载约定一致；
# 跨架构打包：make package-agent GOARCH=arm64）。
# E-18b：ferry-quic sidecar 随包发布（--quic 安装位下载同名约定；不用 QUIC 的部署不装）。
package-agent: build-agent build-quic
	cd bin && tar -czf ferry-agent_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz ferry-agent
	cd bin && sha256sum ferry-agent_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz > ferry-agent_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz.sha256
	cd bin && tar -czf ferry-quic_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz ferry-quic
	cd bin && sha256sum ferry-quic_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz > ferry-quic_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz.sha256
	@ls -l bin/ferry-agent_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz* bin/ferry-quic_$(AGENT_VERSION)_linux_$(GOARCH).tar.gz*

build-dash:
	cd dash && pnpm build

build-panel:
	cd panel && pnpm build

test:
	cd server && go test ./...
	cd agent && go test ./...
	cd packages/agentproto && go test ./...
	cd packages/payment && go test ./...
	cd payments && go test ./...
	cd dash && pnpm build
	cd panel && pnpm build

# 需本机 docker：起临时 PostgreSQL 跑存储三方言集成测试
test-pg:
	docker start ferry-pg-test 2>/dev/null || docker run -d --name ferry-pg-test -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ferry -p 54329:5432 postgres:15-alpine
	@sleep 3
	cd server && FERRY_TEST_PG_DSN="host=127.0.0.1 port=54329 user=postgres password=postgres dbname=ferry sslmode=disable" go test -count=1 ./internal/storage/

# 前后端并行起：make -j2 内 Ctrl-C 同时退出
dev:
	$(MAKE) -j2 dev-server dev-dash dev-panel

dev-server:
	cd server && go run ./cmd/ferry

dev-dash:
	cd dash && pnpm dev

dev-panel:
	cd panel && pnpm dev

fmt:
	gofmt -w server agent packages/agentproto
