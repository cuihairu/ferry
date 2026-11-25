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

// InstanceAction is the L3 one-click new instance: provision a replacement
// instance through the recovery template's supply pipeline (the same
// PrepareLaunch path as dash's one-click launch: pre-issue the node row, install the agent via cloud-init, join the pool on first connect). If the template/credentials are unavailable it fails with a reason and the orchestration advances.
type InstanceAction struct {
	Manager    *provision.Manager
	Store      *secret.Store
	TemplateID uint
	// Launch is the panel-injected launch-params assembler (panel URL/download base/token
	// issuing), reusing the same environment conventions as the handler side.
	Launch func(name string) provision.LaunchParams
}

// Name is the action trace name (Recovery.Action).
func (a *InstanceAction) Name() string { return "new_instance" }

// Run provisions a replacement machine synchronously: PrepareLaunch (create the node row and job) → Execute → write back.
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

// lastLine takes the tail log line as the error summary (the full tail is kept in provision_jobs traces).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
