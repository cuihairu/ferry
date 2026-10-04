module github.com/cuihairu/ferry/agent

go 1.27.1

replace github.com/cuihairu/ferry/packages/agentproto => ../packages/agentproto

require (
	github.com/cuihairu/ferry/packages/agentproto v0.0.0
	github.com/gorilla/websocket v1.5.3
)
