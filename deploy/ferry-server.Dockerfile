# ferry-server：静态编译的单二进制，sqlite 默认落 /data。
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum server/
COPY packages/ packages/
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
