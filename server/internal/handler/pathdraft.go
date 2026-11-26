package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Candidate path lifecycle (F-1016~F-1019). A draft is created from a
// previewed entry/landing pair, reviewed and confirmed into a real Path, or
// rejected. Automatic provisioning additionally verifies connectivity against
// fresh tunnel probe verdicts; a failed verification rolls back the path it
// just created so no half-provisioned route survives.

// draftState values are terminal: a draft is confirmed or rejected exactly once.
const (
	draftPending   = "pending"
	draftConfirmed = "confirmed"
	draftRejected  = "rejected"
)

// pathDraftInput is the operator's draft payload. It reuses pathInput's
// validation rules by embedding the same field set (pending rows are created
// through validatePathInput after their preview), and adds the reason/operator
// audit fields.
type pathDraftInput struct {
	pathInput
	Reason   string `json:"reason"`
	Operator string `json:"operator"`
}

// createPathDraft serves POST /api/paths/drafts: validate the prospective path
// exactly as createPath does, but keep it as a pending row.
func (h *Handler) createPathDraft(c *gin.Context) {
	var in pathDraftInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.validatePathInput(in.pathInput)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	operator := strings.TrimSpace(in.Operator)
	if operator == "" {
		operator, _ = h.auditActor(c)
	}
	d := storage.PendingPath{
		Name: p.Name, EntryNodeID: p.EntryNodeID, LandingNodeID: p.LandingNodeID,
		RelayNodeID: p.RelayNodeID, Transport: p.Transport, Region: p.Region,
		Direction: p.Direction, ProbeImportance: p.ProbeImportance,
		Reason: strings.TrimSpace(in.Reason), State: draftPending,
		CreatedBy: operator,
	}
	if err := h.db.Create(&d).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, d)
}

// listPathDrafts serves GET /api/paths/drafts?state=pending.
func (h *Handler) listPathDrafts(c *gin.Context) {
	q := h.db.Model(&storage.PendingPath{})
	if s := strings.TrimSpace(c.Query("state")); s != "" {
		q = q.Where("state = ?", s)
	}
	var rows []storage.PendingPath
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"drafts": rows})
}

// findPathDraft loads a draft; a missing row is a 404.
func (h *Handler) findPathDraft(id string) (*storage.PendingPath, error) {
	var d storage.PendingPath
	if err := h.db.First(&d, id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// pathDraftInputFrom converts a pending row into the pathInput the shared
// validation expects; the draft was already validated at creation, but
// confirming re-checks topology so a later node deletion cannot slip through.
func pathDraftInputFrom(d storage.PendingPath) pathInput {
	enabled := true
	return pathInput{
		Name: d.Name, EntryNodeID: &d.EntryNodeID, LandingNodeID: &d.LandingNodeID,
		RelayNodeID: d.RelayNodeID, Transport: d.Transport, Region: d.Region,
		Direction: d.Direction, Enabled: &enabled, ProbeImportance: d.ProbeImportance,
	}
}

// confirmPathDraft serves POST /api/paths/drafts/:id/confirm (F-1016): create
// the actual Path from the draft and mark the draft confirmed in one
// transaction. A duplicate name rolls the whole thing back.
func (h *Handler) confirmPathDraft(c *gin.Context) {
	d, err := h.findPathDraft(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errNotFound)
		return
	}
	if d.State != draftPending {
		fail(c, http.StatusConflict, errors.New("draft is not pending"))
		return
	}
	p, err := h.validatePathInput(pathDraftInputFrom(*d))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p.Note = "pending-path:" + strconv.FormatUint(uint64(d.ID), 10)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		return tx.Model(&storage.PendingPath{}).Where("id = ? AND state = ?", d.ID, draftPending).
			Updates(map[string]any{"state": draftConfirmed, "path_id": p.ID}).Error
	})
	if err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path name already exists"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"draft": d.ID, "path": p})
}

// rejectPathDraft serves POST /api/paths/drafts/:id/reject: keep the draft for
// audit but mark it rejected so it cannot be confirmed later.
func (h *Handler) rejectPathDraft(c *gin.Context) {
	d, err := h.findPathDraft(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errNotFound)
		return
	}
	if d.State != draftPending {
		fail(c, http.StatusConflict, errors.New("draft is not pending"))
		return
	}
	if err := h.db.Model(&storage.PendingPath{}).Where("id = ? AND state = ?", d.ID, draftPending).
		Update("state", draftRejected).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"draft": d.ID, "state": draftRejected})
}

// verifyNewPath checks whether a fresh tunnel probe from the entry to the
// landing reports the path healthy (F-1018). No probe data yet is "unknown",
// not a pass -- automatic provisioning waits for an explicit healthy verdict.
func verifyNewPath(db *gorm.DB, p storage.Path, now time.Time) (bool, string, error) {
	var reports []storage.ProbeReport
	if err := db.Where("node_id = ? AND target_kind = ? AND target_node_id = ? AND probed_at > ?",
		p.EntryNodeID, "tunnel", p.LandingNodeID, now.Add(-pathhealth.ProbeFresh)).
		Order("probed_at DESC, id DESC").Find(&reports).Error; err != nil {
		return false, "", err
	}
	if len(reports) == 0 {
		return false, "no fresh tunnel probe verdict", nil
	}
	r := reports[0]
	if r.Verdict == "healthy" && r.Reachable {
		return true, "tunnel probe healthy", nil
	}
	return false, "tunnel probe " + r.Verdict, nil
}

// autoProvisionInput describes the entry/landing pair the scheduler picked.
type autoProvisionInput struct {
	Name            string `json:"name"`
	EntryNodeID     uint   `json:"entry_node_id"`
	LandingNodeID   uint   `json:"landing_node_id"`
	RelayNodeID     *uint  `json:"relay_node_id"`
	Transport       string `json:"transport"`
	Region          string `json:"region"`
	Direction       string `json:"direction"`
	ProbeImportance string `json:"probe_importance"`
}

// autoProvisionPath serves POST /api/paths/provision (F-1017/F-1018/F-1019):
// create the path, verify connectivity from fresh tunnel probe verdicts, and
// delete the path when verification fails. The rollback covers every row this
// operation created, so a failed provision leaves no partial resource behind.
func (h *Handler) autoProvisionPath(c *gin.Context) {
	var in autoProvisionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	pi := pathInput{
		Name: in.Name, EntryNodeID: &in.EntryNodeID, LandingNodeID: &in.LandingNodeID,
		RelayNodeID: in.RelayNodeID, Transport: in.Transport, Region: in.Region,
		Direction: in.Direction, ProbeImportance: in.ProbeImportance,
	}
	p, err := h.validatePathInput(pi)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p.Note = "auto-provision"

	// The path row is committed first so the agent can observe it and a probe
	// can run; a failed verification then deletes exactly this row.
	if err := h.db.Create(&p).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path name already exists"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	ok, reason, err := verifyNewPath(h.db, p, time.Now())
	if err != nil {
		_ = h.db.Delete(&storage.Path{}, p.ID)
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		if err := h.db.Delete(&storage.Path{}, p.ID).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":               "connectivity verification failed: " + reason,
			"rolled_back_path_id": p.ID,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"path": p, "verified": true, "reason": reason})
}
