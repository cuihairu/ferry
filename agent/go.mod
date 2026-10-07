module github.com/cuihairu/ferry/agent

go 1.27.1

replace github.com/cuihairu/ferry/packages/agentproto => ../packages/agentproto

require (
	github.com/cuihairu/ferry/packages/agentproto v0.0.0
	github.com/gorilla/websocket v1.5.3
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
)
