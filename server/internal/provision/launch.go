// Server-launch orchestration (shared by OS-3/OS-4/BR-1): node pre-issuance + cloud-init
// preparation + job tracing on one path -- the one-click launch from the dash and the L3
// recovery launch of a new machine when blocked share PrepareLaunch. Split: PrepareLaunch
// only prepares (synchronous, seconds); Execute runs (minutes) and is backgrounded by the caller; FinishJob writes back the terminal state.
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

// LaunchParams is the full input for one server launch.
type LaunchParams struct {
	Template storage.ProvisionTemplate
	Provider storage.Provider // raw row; the AccessKey ciphertext is decrypted inside PrepareLaunch
	Store    *secret.Store    // secret entry point (R24); error out directly if empty/disabled
	// InstanceName is the instance name (label), doubling as node name and agent_id.
	InstanceName string
	// PanelWSURL is the agent ingress address (assembled by provision.PanelWSURL);
	// AgentBase/AgentVersion set the cloud-init download conventions.
	PanelWSURL   string
	AgentBase    string
	AgentVersion string
	// NewToken issues a node token (the panel's randomToken is injected; provision never mints tokens itself).
	NewToken func() (string, error)
}

// Prepared is the result of launch preparation: the job row is created (running), the node row
// pre-issued (status=provisioning, first-contact registration is closed out by the intake pipeline),
type Prepared struct {
	Job    *storage.ProvisionJob
	Node   storage.Node
	Params ExecParams
}

// PrepareLaunch prepares a server launch: decrypt credentials -> pre-issue/reuse the node row
// (OS-3 reuses by name so the token does not churn) -> render cloud-init -> create a running job.
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

// FinishJob writes back the terminal state of supply execution (called after Execute, same convention as Apply).
func (m *Manager) FinishJob(jobID uint, status, logTail string) {
	m.db.Model(&storage.ProvisionJob{}).Where("id = ?", jobID).
		Updates(map[string]any{"status": status, "log": logTail, "finished_at": time.Now()})
}

// prepareNode pre-issues/reuses the supply node row: if a node with the same name exists it is reused (token unchanged,
// re-launching creates no duplicate row and main.tf does not churn over a new token); otherwise a row is created
// with metadata from the template and marked meta_init (the panel takes over, registration reports do not overwrite).
// status=provisioning means supplying: excluded from subscriptions and allocation (sub/alloc queries filter it out),
// turned online by the intake pipeline after first contact; orphan rows from a failed launch are adopted by the state machine.
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
	// Enabled must be true: authNode only accepts enabled nodes, and first-contact registration depends on it.
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
		Config:            compactJSON(tpl.Config), // OS-4: the config template lands on the node row with the launch
		MetaInit:          true,
	}
	if err := m.db.Create(&node).Error; err != nil {
		return storage.Node{}, err
	}
	return node, nil
}

// compactJSON normalizes the config template into compact JSON text; an empty string falls back to {} (same convention as the node page).
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
