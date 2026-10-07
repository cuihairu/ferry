package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// InstanceAction 是 L3 一键开新机：按恢复模板走供给流水线开替换实例
// （与 dash 一键开服同一条 PrepareLaunch 路径，预签发节点、cloud-init
// 装 agent、首连接入池）。模板/凭证不可用时失败留原因，由编排推进。
type InstanceAction struct {
	Manager    *provision.Manager
	Store      *secret.Store
	TemplateID uint
	// Launch 是面板注入的开服参数装配（面板地址/下载口径/令牌签发），
	// 复用 handler 侧同一套环境口径。
	Launch func(name string) provision.LaunchParams
}

// Name 是动作留痕名（Recovery.Action）。
func (a *InstanceAction) Name() string { return "new_instance" }

// Run 同步开一台替换机：PrepareLaunch（建节点行与 job）→ Execute → 回填。
func (a *InstanceAction) Run(ctx context.Context, node storage.Node) error {
	if a.Manager == nil || a.Launch == nil || a.TemplateID == 0 {
		return errors.New("recovery: new_instance action not configured (set FERRY_RECOVERY_TEMPLATE_ID)")
	}
	p := a.Launch(node.Name + "-r")
	var tpl storage.ProvisionTemplate
	if err := a.Manager.DB().First(&tpl, a.TemplateID).Error; err != nil {
		return fmt.Errorf("recovery template %d: %w", a.TemplateID, err)
	}
	var prov storage.Provider
	if err := a.Manager.DB().First(&prov, tpl.ProviderID).Error; err != nil {
		return errors.New("recovery: template provider missing")
	}
	if !prov.Enabled {
		return errors.New("recovery: template provider disabled")
	}
	p.Template, p.Provider, p.Store = tpl, prov, a.Store
	p.InstanceName = node.Name + "-r"

	prep, err := a.Manager.PrepareLaunch(p)
	if err != nil {
		return err
	}
	status, logTail := a.Manager.Execute(ctx, prep.Params)
	a.Manager.FinishJob(prep.Job.ID, status, logTail)
	if status != "ok" {
		return fmt.Errorf("recovery apply failed: %s", lastLine(logTail))
	}
	return nil
}

// lastLine 取日志尾行作错误摘要（完整尾部在 provision_jobs 留痕）。
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
