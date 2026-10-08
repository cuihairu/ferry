module github.com/cuihairu/ferry/agent

go 1.27.1

replace github.com/cuihairu/ferry/packages/agentproto => ../packages/agentproto

require (
	github.com/cuihairu/ferry/packages/agentproto v0.0.0
	github.com/gorilla/websocket v1.5.3
	github.com/quic-go/quic-go v0.63.0
	github.com/shirou/gopsutil/v4 v4.26.9
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.57.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/power-devops/perfstat v0.0.0-20260805114148-88456608a4f6 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)
