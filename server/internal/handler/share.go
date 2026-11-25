package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
)

// nodeShare returns a single node's share link (P1-9, matching 3x-ui
// /links/:email's single-object shape): reuses the subscription-side ShareLink
// constructor; the admin UI renders the QR code from the link itself.
func (h *Handler) nodeShare(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	link, err := sub.ShareLink(n)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"node_id": n.ID, "name": n.Name, "link": link})
}
