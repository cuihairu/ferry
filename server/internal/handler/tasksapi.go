package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/tasks"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Unified task surface (F-1602~F-1615): one list/detail/enqueue/cancel/retry
// set over the shared tasks ledger, plus the built-in consumers: pool
// operations (node.suspend / node.resume, F-1628/F-1629) and the agent-link
// operations (agent.restart F-1621, relay.restart F-1622, agent.rollback
// F-1626, node.reprobe F-1627) that ride the panel->agent request/reply path.

// agentOpTimeout covers one panel->agent request/reply round for the task
// consumers (the runner's own per-attempt timeout is the outer bound).
const agentOpTimeout = 15 * time.Second

// agentRequest sends one panel->agent request and decodes the reply; offline
// and timeout surface as plain errors so the task runner retries with backoff.
func (h *Handler) agentRequest(nodeID int64, msgType string, payload, reply any) error {
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("task-%d-%d", nodeID, time.Now().UnixNano()), msgType, payload)
	if err != nil {
		return err
	}
	rep, err := h.hub.Request(nodeID, env, agentOpTimeout)
	if err != nil {
		return err
	}
	return rep.Decode(reply)
}

// registerTaskConsumers wires the built-in task kinds onto the shared runner.
func registerTaskConsumers(h *Handler, mgr *tasks.Manager) {
	if mgr == nil {
		return
	}
	db := h.db
	// node.suspend (F-1628) / node.resume (F-1629): the same pool rules as the
	// manual endpoints (re-suspending is an error, resume requires the suspended
	// or standby state), but as tracked tasks with retry/backoff and an audit
	// trail instead of an untracked synchronous flip.
	mgr.Register("node.suspend", tasks.Definition{
		MaxAttempts: 2,
		Timeout:     30 * time.Second,
		Depends: func(ctx context.Context, t storage.Task) error {
			var n storage.Node
			if err := db.First(&n, t.RefID).Error; err != nil {
				return err
			}
			if n.Role != "entry" && n.Role != "both" {
				return errors.New("node is not an entry")
			}
			return nil
		},
		Run: func(ctx context.Context, t *storage.Task) error {
			_, err := pool.Suspend(db, t.RefID, nil)
			if err != nil && errors.Is(err, pool.ErrAlreadySuspended) {
				// Idempotent semantics: already suspended is success for the task form.
				return nil
			}
			if err == nil && pool.OnChange != nil {
				go pool.OnChange(db)
			}
			return err
		},
	})
	mgr.Register("node.resume", tasks.Definition{
		MaxAttempts: 2,
		Timeout:     30 * time.Second,
		Depends: func(ctx context.Context, t storage.Task) error {
			var n storage.Node
			if err := db.First(&n, t.RefID).Error; err != nil {
				return err
			}
			return nil
		},
		Run: func(ctx context.Context, t *storage.Task) error {
			_, err := pool.Resume(db, t.RefID, nil)
			if err != nil && errors.Is(err, pool.ErrNotSuspended) {
				return nil // already in pool: success for the task form
			}
			if err == nil && pool.OnChange != nil {
				go pool.OnChange(db)
			}
			return err
		},
	})

	// nodeExists is the shared precheck for the agent-link kinds: the node row
	// must exist (RefID is the node id for every consumer below).
	nodeExists := func(ctx context.Context, t storage.Task) error {
		var n storage.Node
		return db.First(&n, t.RefID).Error
	}

	// agent.restart (F-1621): the plain restart command rides the agent link;
	// the agent acks before re-exec'ing itself, so OK means accepted, and the
	// reconnect after the restart refreshes its online status.
	mgr.Register("agent.restart", tasks.Definition{
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Depends:     nodeExists,
		Run: func(ctx context.Context, t *storage.Task) error {
			var ack agentproto.AgentRestartAck
			if err := h.agentRequest(int64(t.RefID), agentproto.MsgAgentRestart, struct{}{}, &ack); err != nil {
				return err
			}
			if !ack.OK {
				return errors.New("agent restart refused: " + ack.Error)
			}
			return nil
		},
	})

	// relay.restart (F-1622): stop then start the managed relay proc over the
	// same proc_ctl channel as the manual endpoints (stop waits for exit
	// agent-side, so the pair is a true restart cycle regardless of the proc's
	// reload policy).
	mgr.Register("relay.restart", tasks.Definition{
		MaxAttempts: 3,
		Timeout:     90 * time.Second,
		Depends:     nodeExists,
		Run: func(ctx context.Context, t *storage.Task) error {
			id := int64(t.RefID)
			if _, _, err := h.procCtl(id, "relay", agentproto.ProcActionStop); err != nil {
				return err
			}
			ack, _, err := h.procCtl(id, "relay", agentproto.ProcActionStart)
			if err != nil {
				return err
			}
			if !ack.OK {
				return errors.New("relay start failed: " + ack.Error)
			}
			return nil
		},
	})

	// agent.rollback (F-1626): panel-initiated rollback to the agent's backup
	// binary (the watchdog path stays automatic); a missing backup fails the
	// attempt with a clear error instead of silently doing nothing.
	mgr.Register("agent.rollback", tasks.Definition{
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Depends:     nodeExists,
		Run: func(ctx context.Context, t *storage.Task) error {
			var ack agentproto.AgentRollbackAck
			if err := h.agentRequest(int64(t.RefID), agentproto.MsgAgentRollback, struct{}{}, &ack); err != nil {
				return err
			}
			if !ack.OK {
				return errors.New("agent rollback failed: " + ack.Error)
			}
			return nil
		},
	})

	// node.reprobe (F-1627): kick the agent's probe loops into an immediate
	// cycle; the ack carries the kicked loop count, conclusions land via the
	// usual report channel.
	mgr.Register("node.reprobe", tasks.Definition{
		MaxAttempts: 3,
		Timeout:     30 * time.Second,
		Depends:     nodeExists,
		Run: func(ctx context.Context, t *storage.Task) error {
			var ack agentproto.ProbeNowAck
			if err := h.agentRequest(int64(t.RefID), agentproto.MsgProbeNow, struct{}{}, &ack); err != nil {
				return err
			}
			if !ack.OK {
				return errors.New("reprobe failed: " + ack.Error)
			}
			return nil
		},
	})
}

// taskActor names the caller for the task's actor column (F-1613).
func (h *Handler) taskActor(c *gin.Context) string {
	name, _ := h.auditActor(c)
	if name == "" || name == "unknown" {
		return "admin"
	}
	return name
}

// listTasks serves GET /api/tasks (kind/status filters, newest first).
func (h *Handler) listTasks(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.Task{}).Order("id DESC").Limit(limit)
	if v := c.Query("kind"); v != "" {
		q = q.Where("kind = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	out := []storage.Task{}
	if err := q.Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// getTask serves GET /api/tasks/:id (row includes stage/progress/log/result).
func (h *Handler) getTask(c *gin.Context) {
	var t storage.Task
	if err := h.db.First(&t, c.Param("id")).Error; err != nil {
		replyFind(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

type taskEnqueueInput struct {
	Kind           string `json:"kind"`
	RefID          uint   `json:"ref_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// enqueueTask serves POST /api/tasks: enqueue a registered kind as a tracked
// task (idempotency key optional, F-1608; unknown kinds are a 400 so no row is
// ever minted that nothing will run).
func (h *Handler) enqueueTask(c *gin.Context) {
	var in taskEnqueueInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Kind == "" {
		fail(c, http.StatusBadRequest, errors.New("kind is required"))
		return
	}
	row, err := h.tasks.Enqueue(in.Kind, in.RefID, in.IdempotencyKey, h.taskActor(c))
	if err != nil {
		if errors.Is(err, tasks.ErrUnknownKind) {
			fail(c, http.StatusBadRequest, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, row)
}

// cancelTask serves POST /api/tasks/:id/cancel (F-1607).
func (h *Handler) cancelTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("task not found"))
		return
	}
	status, err := h.tasks.Cancel(uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// retryTask serves POST /api/tasks/:id/retry (F-1611): manual re-queue of a
// failed/dead/cancelled task with the full attempt budget restored.
func (h *Handler) retryTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("task not found"))
		return
	}
	row, err := h.tasks.Retry(uint(id), h.taskActor(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, row)
}
