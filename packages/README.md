# packages

前后端/插件共享的协议契约与常量包，server 侧以 go.mod replace 引用（`server/go.mod`）。

| 包 | 内容 | 引用方 |
| --- | --- | --- |
| `agentproto` | 面板与 ferry-agent 的消息契约（WebSocket + JSON，消息类型常量与信封，见 `envelope.go`/`messages.go`） | server、agent |
| `payment` | 收款 Provider 契约与注册表（server 与 payments/ 插件共用，见《支付设计》§1） | server、payments |
| `costref` | 成本参考库插件位（参考牌价 Source 契约 + 手录价偏差提示 + 公开价格表来源，手录为准只提示不改价，见《套餐与成本设计》§4） | server |
