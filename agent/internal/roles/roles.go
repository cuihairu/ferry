// Package roles draws the boundary between the agent's "minimal core" and its "role components" (E-4).
//
// Core (resident with the main process, targeting a <=10MB static binary):
//
//	app/link (registration heartbeat and message dispatch), config, procs (process management),
//	configd (config push and rollback), traffic (traffic load reporting), host (local load sampling),
//	certwatch (certificate expiry collection).
//
// Role components (assembled per the node's role; a crash must never take the heartbeat down):
//
//	probe (edge probing), relay (the relay data plane), tunnel (transport plugins),
//	speedtest (automatic speed-test calibration).
//
// Components that run as standalone processes are started through a -role subcommand, raised by the core
// procs.Manager as managed processes (E-5/E-7), interacting with the core only through local config files.
package roles

// Component describes one role component.
type Component struct {
	// Name matches the -role subcommand name.
	Name string
	// Standalone being true means it can run as a standalone process (-role <name>).
	Standalone bool
	// Desc is a one-line description.
	Desc string
}

// Components is the registry of shipped role components.
var Components = []Component{
	{Name: "probe", Standalone: true, Desc: "边缘探测器：隧道/出口/互探结论上报"},
	{Name: "relay", Standalone: true, Desc: "relay 数据面：入口角色隧道转发"},
	{Name: "tunnel", Standalone: false, Desc: "传输插件：隧道接口与 TLS 伪装实现（库形态，被 relay 引用）"},
	{Name: "speedtest", Standalone: false, Desc: "自动测速校准：注册后轻量测速（库形态，被核心调用）"},
}

// Known decides whether a name is a registered role component.
func Known(name string) bool {
	for _, c := range Components {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Standalone decides whether a component supports running as a standalone process.
func Standalone(name string) bool {
	for _, c := range Components {
		if c.Name == name {
			return c.Standalone
		}
	}
	return false
}
