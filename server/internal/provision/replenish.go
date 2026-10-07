package provision

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// Replenisher 是池空保底（BR-3）：入口池一个可用节点都没有时，按模板
// 自动开新机（同 PrepareLaunch 开服路径，OS-4 流水线自动入池）。
// 与恢复流水线的 L3 互补：L3 是「某节点被封的替换」，这里是「池整体
// 空掉的兜底」。防重：有供给中的入口（provisioning）或在途 apply job
// 就不动手，避免连环开机。模板 0=关闭。
type Replenisher struct {
	Manager    *Manager
	Store      *secret.Store
	TemplateID uint
	// Launch 是面板注入的开服参数装配（同恢复 L3，复用面板地址/下载口径）。
	Launch func(name string) LaunchParams
}

// Enabled 未配置模板时保底不开。
func (r *Replenisher) Enabled() bool { return r != nil && r.TemplateID > 0 && r.Launch != nil }

// Sweep 执行一轮：池空且无在途供给 → 后台开一台替换机。返回是否触发。
func (r *Replenisher) Sweep(ctx context.Context, logger *log.Logger) (bool, error) {
	if !r.Enabled() {
		return false, nil
	}
	if logger == nil {
		logger = log.Default()
	}
	db := r.Manager.DB()
	var n int64
	if err := db.Model(&storage.Node{}).
		Where("enabled = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
			true, "entry", "both", pool.StateActive, "provisioning").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil // 池未空
	}
	// 防重：供给中的入口或在途 apply 都算在途。
	if err := db.Model(&storage.Node{}).
		Where("enabled = ? AND role IN (?, ?) AND status = ?",
			true, "entry", "both", "provisioning").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if err := db.Model(&storage.ProvisionJob{}).
		Where("action = ? AND status = ?", "apply", "running").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}

	var tpl storage.ProvisionTemplate
	if err := db.First(&tpl, r.TemplateID).Error; err != nil {
		return false, fmt.Errorf("replenish template %d: %w", r.TemplateID, err)
	}
	var prov storage.Provider
	if err := db.First(&prov, tpl.ProviderID).Error; err != nil {
		return false, errors.New("replenish: template provider missing")
	}
	if !prov.Enabled {
		return false, errors.New("replenish: template provider disabled")
	}
	name := fmt.Sprintf("pool-%d", time.Now().Unix())
	p := r.Launch(name)
	p.Template, p.Provider, p.Store = tpl, prov, r.Store
	p.InstanceName = name
	prep, err := r.Manager.PrepareLaunch(p)
	if err != nil {
		return false, err
	}
	logger.Printf("replenish: pool empty, launching %s from template %d", name, r.TemplateID)
	go func() {
		status, logTail := r.Manager.Execute(context.Background(), prep.Params)
		r.Manager.FinishJob(prep.Job.ID, status, logTail)
		logger.Printf("replenish: %s finished with %s", name, status)
	}()
	return true, nil
}

// ReplenishLoop 周期执行池空保底直到 ctx 取消。
func ReplenishLoop(ctx context.Context, r *Replenisher, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := r.Sweep(ctx, logger); err != nil {
			logger.Printf("replenish sweep: %v", err)
		}
		t.Reset(interval)
	}
}
