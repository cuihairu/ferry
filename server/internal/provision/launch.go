// 开服编排（OS-3/OS-4/BR-1 共用）：节点预签发 + cloud-init 准备 + job 留痕
// 的同一条路径——dash 一键开服与被封恢复 L3 开新机走同一套 PrepareLaunch。
// 拆分口径：PrepareLaunch 只做准备（同步、秒级），Execute 执行（分钟级）
// 由调用方放后台，FinishJob 回填终态。
package provision

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// LaunchParams 是一次开服的全部输入。
type LaunchParams struct {
	Template storage.ProvisionTemplate
	Provider storage.Provider // 原始行，AccessKey 密文在 PrepareLaunch 内解密
	Store    *secret.Store    // 机密入口（R24），空/未启用直接报错
	// InstanceName 是实例名（label）兼节点名与 agent_id。
	InstanceName string
	// PanelWSURL 是 agent 接入地址（provision.PanelWSURL 拼装），
	// AgentBase/AgentVersion 是 cloud-init 下载口径。
	PanelWSURL   string
	AgentBase    string
	AgentVersion string
	// NewToken 签发节点令牌（面板侧 randomToken 注入，provision 不自造令牌）。
	NewToken func() (string, error)
}

// Prepared 是开服准备结果：job 行已建（running）、节点行已预签发
// （status=provisioning，首连注册由入池流水线收尾），执行参数齐备。
type Prepared struct {
	Job    *storage.ProvisionJob
	Node   storage.Node
	Params ExecParams
}

// PrepareLaunch 预备一次开服：解密凭证 → 预签发/复用节点行（OS-3 同名复用
// token 不抖动）→ 渲染 cloud-init → 建 running job。同步秒级，不执行 tofu。
func (m *Manager) PrepareLaunch(p LaunchParams) (*Prepared, error) {
	if p.Store == nil || !p.Store.Enabled() {
		return nil, errors.New("provision: secret master key not configured (set FERRY_SECRET_KEY)")
	}
	apiKey, err := DecryptKey(p.Provider, p.Store)
	if err != nil {
		return nil, err
	}
	if p.NewToken == nil {
		return nil, errors.New("provision: token issuer not provided")
	}
	node, err := m.prepareNode(p.InstanceName, p.Template, p.NewToken)
	if err != nil {
		return nil, fmt.Errorf("provision: prepare node: %w", err)
	}
	cfgJSON, err := AgentConfigJSON(AgentConfigFile{
		PanelURL: p.PanelWSURL, AgentID: p.InstanceName, Token: node.Token,
	})
	if err != nil {
		return nil, err
	}
	job := storage.ProvisionJob{
		TemplateID: p.Template.ID, TemplateName: p.Template.Name, Action: "apply", Status: "running",
	}
	if err := m.db.Create(&job).Error; err != nil {
		return nil, err
	}
	return &Prepared{
		Job:  &job,
		Node: node,
		Params: ExecParams{
			Template: p.Template, ProviderType: p.Provider.Type, APIKey: apiKey,
			InstanceName: p.InstanceName, Action: "apply",
			UserData: CloudInit(cfgJSON, p.AgentBase, p.AgentVersion),
		},
	}, nil
}

// FinishJob 回填供给执行终态（Execute 之后调用，与 Apply 同口径）。
func (m *Manager) FinishJob(jobID uint, status, logTail string) {
	m.db.Model(&storage.ProvisionJob{}).Where("id = ?", jobID).
		Updates(map[string]any{"status": status, "log": logTail, "finished_at": time.Now()})
}

// prepareNode 预签发/复用供给节点行：同名节点已存在则复用（token 不变，
// 重放开服不重复建行、main.tf 不因新 token 抖动）；否则建行，元数据取自
// 模板并置 meta_init（面板接管，注册上报不覆盖）。status=provisioning
// 表示供给中：不入订阅、不参与分配（sub/alloc 查询排除），首连注册后
// 经入池流水线转 online；开服失败遗留的孤儿行由后续状态机收编。
func (m *Manager) prepareNode(name string, tpl storage.ProvisionTemplate, newToken func() (string, error)) (storage.Node, error) {
	var node storage.Node
	err := m.db.Where("name = ?", name).First(&node).Error
	if err == nil {
		return node, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return node, err
	}
	token, err := newToken()
	if err != nil {
		return node, err
	}
	// Enabled 必须为真：authNode 只认启用节点，首连注册依赖它。
	node = storage.Node{
		Name:              name,
		Port:              443,
		Protocol:          "vless",
		Enabled:           true,
		Token:             token,
		Status:            "provisioning",
		Role:              tpl.Role,
		Direction:         tpl.Direction,
		LineType:          tpl.LineType,
		Region:            tpl.Region,
		Transport:         tpl.Transport,
		BillingType:       tpl.BillingType,
		MonthlyCostCents:  tpl.MonthlyCostCents,
		TrafficPriceCents: tpl.TrafficPriceCents,
		Config:            compactJSON(tpl.Config), // OS-4：配置模板随开服落到节点行
		MetaInit:          true,
	}
	if err := m.db.Create(&node).Error; err != nil {
		return storage.Node{}, err
	}
	return node, nil
}

// compactJSON 把配置模板规整成紧凑 JSON 文本；空串回 {}（与节点页同口径）。
func compactJSON(cfg string) string {
	trimmed := strings.TrimSpace(cfg)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if json.Unmarshal([]byte(trimmed), &v) != nil {
		return trimmed
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return trimmed
	}
	return string(compact)
}
