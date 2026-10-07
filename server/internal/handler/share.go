package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
)

// nodeShare 返回单节点分享链接（P1-9，对齐 3x-ui /links/:email 的单对象口径）：
// 复用订阅侧 ShareLink 构造器，二维码由管理端按链接自行渲染。
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
