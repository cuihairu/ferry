// Package roles 划定 agent「核心最小集」与「角色组件」的边界（E-4）。
//
// 核心（随主进程常驻，目标 ≤10MB 静态二进制）：
//
//	app/link（注册心跳与消息分发）、config（配置）、procs（进程管理）、
//	configd（配置下发与回滚）、traffic（流量负载上报）、host（本机负载采样）、
//	certwatch（证书到期采集）。
//
// 角色组件（按节点 role 按需装配，崩溃不得带崩心跳）：
//
//	probe（边缘探测）、relay（relay 数据面）、tunnel（传输插件）、
//	speedtest（自动测速校准）。
//
// 独立进程形态的组件经 -role 子命令启动，由核心 procs.Manager 以被管
// 进程方式拉起（E-5/E-7），与核心只经本机配置文件交互。
package roles

// Component 描述一个角色组件。
type Component struct {
	// Name 与 -role 子命令名一致。
	Name string
	// Standalone 为真表示可独立进程运行（-role <name>）。
	Standalone bool
	// Desc 一句话说明。
	Desc string
}

// Components 是已落地的角色组件注册表。
var Components = []Component{
	{Name: "probe", Standalone: true, Desc: "边缘探测器：隧道/出口/互探结论上报"},
	{Name: "relay", Standalone: true, Desc: "relay 数据面：入口角色隧道转发"},
	{Name: "tunnel", Standalone: false, Desc: "传输插件：隧道接口与 TLS 伪装实现（库形态，被 relay 引用）"},
	{Name: "speedtest", Standalone: false, Desc: "自动测速校准：注册后轻量测速（库形态，被核心调用）"},
}

// Known 判断名字是否为已注册的角色组件。
func Known(name string) bool {
	for _, c := range Components {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Standalone 判断组件是否支持独立进程运行。
func Standalone(name string) bool {
	for _, c := range Components {
		if c.Name == name {
			return c.Standalone
		}
	}
	return false
}
