package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/oper"
	"github.com/gin-gonic/gin"
)

// Cloud operation task API (F-0361~F-0384): create single/batch operations
// with state-machine tracking, idempotency keys and high-risk confirmation,
// list/detail/log per task, and cancel. The audit middleware records the
// actor and request snapshot (F-0384).

// operActor mirrors auditActor but stays within the apiAuth credential set
// (admin JWT or admin API token): operations are admin-plane tasks.
func (h *Handler) operActor(c *gin.Context) (string, string) {
	if id, ok := c.Get("adminUserID"); ok && id != "" {
		if s, ok2 := c.Get("apiUser"); ok2 {
			if str, ok3 := s.(string); ok3 && str != "" {
				return str, "admin"
			}
		}
		if s, ok2 := id.(string); ok2 && s != "" {
			return s, "admin"
		}
	}
	if u, ok := c.Get("apiUser"); ok {
		if s, ok2 := u.(string); ok2 && s != "" {
			return s, "api"
		}
	}
	return "unknown", "unknown"
}

func (h *Handler) createCloudOperation(c *gin.Context) {
	var in oper.Request
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// F-1704/F-1705: creating an operation is gated by the op-type permission
	// (power ops need cloud.power, force-stop/rebuild cloud.highrisk); the
	// middleware form cannot see the body, so the check rides here. The
	// high-risk ops' own confirmation flow is unchanged on top of this.
	if !h.opPermCheck(c, in.Op, in.ProviderID) {
		fail(c, http.StatusForbidden, fmt.Errorf("missing permission %s", opPerm[in.Op]))
		return
	}
	actor, actorKind := h.operActor(c)
	id, rows, err := h.operations.Create(c.Request.Context(), in, actor, actorKind, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, oper.ErrConfirmationRequired):
			fail(c, http.StatusConflict, err)
		case errors.Is(err, oper.ErrNotFound):
			fail(c, http.StatusNotFound, err)
		default:
			fail(c, http.StatusBadRequest, err)
		}
		return
	}
	// Execute via the shared client policy; the vendor call is bounded by the
	// adapter timeout, so the response reports the settled state (F-0375).
	_ = h.operations.Run(c.Request.Context(), time.Now())
	if rows[0].BatchID != "" {
		if fresh, err := h.operations.List(c.Request.Context(), rows[0].BatchID, 0, ""); err == nil && len(fresh) > 0 {
			rows = fresh
		}
	} else {
		for i := range rows {
			if fresh, _, err := h.operations.Get(c.Request.Context(), rows[i].ID); err == nil {
				rows[i] = fresh
			}
		}
	}
	// Controlled post-operation linkage (F-0398): a vendor success alone never
	// makes the bound Node available; the cloudlink service queries the real
	// state and applies the mapped Node dimensions (F-0397).
	link := h.cloudlinkService()
	for _, row := range rows {
		_, _, _ = link.ApplyOperation(c.Request.Context(), row.ID, time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "count": len(rows), "batch_id": rows[0].BatchID, "operations": rows})
}

func (h *Handler) listCloudOperations(c *gin.Context) {
	batchID := c.Query("batch_id")
	var resourceID uint
	if s := c.Query("resource_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		resourceID = uint(id)
	}
	rows, err := h.operations.List(c.Request.Context(), batchID, resourceID, c.Query("state"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *Handler) getCloudOperation(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
		return
	}
	row, logs, err := h.operations.Get(c.Request.Context(), uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"operation": row, "logs": logs})
}

func (h *Handler) cancelCloudOperation(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
		return
	}
	if err := h.operations.Cancel(c.Request.Context(), uint(id), time.Now()); err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
