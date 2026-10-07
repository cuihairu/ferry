package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 测速端点（E-8）：agent 注册后下载固定字节测下行容量。
// 公开但设防：单次上限 8MB + 按 IP 30 次/分钟（测速包 4MB，正常注册只打 1 次）。
const (
	speedtestDefault = 4 << 20
	speedtestMin     = 64 << 10
	speedtestMax     = 8 << 20
)

// effectiveDownMbps 判定生效容量（E-8）：无套餐以实测为准；
// 实测与套餐偏差超过 30% 以实测为准（套餐虚标/限速都按实测分配）。
func effectiveDownMbps(plan, measured int) (effective int, accepted bool) {
	if plan <= 0 {
		return measured, true
	}
	if measured <= 0 {
		return plan, false
	}
	dev := float64(measured-plan) / float64(plan)
	if dev < 0 {
		dev = -dev
	}
	if dev > 0.30 {
		return measured, true
	}
	return plan, false
}

// speedtestBytes 下发确定性伪随机字节（xorshift，不可压缩，防中间件压缩虚增）。
func (h *Handler) speedtestBytes(c *gin.Context) {
	if !h.speedLimiter.Allow(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
		return
	}
	n := speedtestDefault
	if v := c.Query("n"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < speedtestMin || parsed > speedtestMax {
			fail(c, http.StatusBadRequest, errors.New("n must be 64KB-8MB"))
			return
		}
		n = parsed
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.Itoa(n))
	c.Status(http.StatusOK)
	state := uint64(0x9E3779B97F4A7C15)
	var buf [32 * 1024]byte
	remain := n
	w := c.Writer
	for remain > 0 {
		chunk := len(buf)
		if remain < chunk {
			chunk = remain
		}
		for i := 0; i < chunk; i++ {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			buf[i] = byte(state)
		}
		if _, err := w.Write(buf[:chunk]); err != nil {
			return
		}
		remain -= chunk
	}
}

// handleCalibrate 接收测速校准，落 speed_measured_mbps/speed_calibrated_at。
func (h *Handler) handleCalibrate(nodeID int64, env agentproto.Envelope) (agentproto.Envelope, bool) {
	var cal agentproto.Calibrate
	if err := env.Decode(&cal); err != nil {
		return agentproto.Envelope{}, false
	}
	var n storage.Node
	if err := h.db.First(&n, nodeID).Error; err != nil {
		return agentproto.Envelope{}, false
	}
	effective, accepted := effectiveDownMbps(n.BwDownMbps, cal.MeasuredDownMbps)
	now := time.Now()
	updates := map[string]any{
		"speed_measured_mbps": cal.MeasuredDownMbps,
		"speed_calibrated_at": &now,
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).Updates(updates).Error; err != nil {
		return agentproto.Envelope{}, false
	}
	note := "within plan tolerance, plan kept"
	if accepted {
		note = "deviation >30%, measured adopted"
	}
	reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgCalibrateAck, agentproto.CalibrateAck{
		Accepted:          accepted,
		EffectiveDownMbps: effective,
		Note:              note,
	})
	return reply, true
}
