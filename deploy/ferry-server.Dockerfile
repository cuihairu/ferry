# ferry-server：静态编译的单二进制，sqlite 默认落 /data。
# 工具链钉补丁号：server/go.mod 声明 `go 1.27.2`（c8e76cb 为清 govulncheck
# 结论抬到 1.27.2，与 CI setup-go 同口径）。`1.27-alpine` 浮动标签解析到
# go1.27.1，低于该声明即 `go mod download` 直接报版本不足而失败——必须
# 用 `1.27.2-alpine`。
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum server/
# server/go.mod 用 replace 指到仓内三个本地模块：agentproto/costref/payment 在
# packages/ 下，payments 是与 packages/ 平级的独立模块（E-31 起）。少 COPY
# payments/ 会在 `go mod download` 直接报 "open /src/payments/go.mod"。
COPY packages/ packages/
COPY payments/ payments/
WORKDIR /src/server
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /out/ferry-server ./cmd/ferry

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/ferry-server /usr/local/bin/ferry-server
ENV FERRY_ADDR=:8080 FERRY_DB_DSN=/data/ferry.db
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/usr/local/bin/ferry-server"]
