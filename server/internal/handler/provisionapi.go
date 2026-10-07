package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 供给执行（OS-2）管理端：模板 plan/apply 受控触发与 job 留痕查询。
// 执行在后台 goroutine 跑（tofu 一轮可达分钟级），接口立即返回 running job；
// 密钥只在 server 侧解密传入执行环境变量，接口与日志不落明文。

func (h *Handler) getProvisionManager() *provision.Manager {
	return provision.New(h.db, h.cfg.TofuBin, h.cfg.TofuWorkdir, nil)
}

type provisionRunInput struct {
	Name string `json:"name"` // 实例名（label），空用模板名
}

// runProvision 是 plan/apply 共用入口：模板与凭证校验 → 后台执行 → running job。
func (h *Handler) runProvision(c *gin.Context, action string) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var tpl storage.ProvisionTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var prov storage.Provider
	if err := h.db.First(&prov, tpl.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("template provider not found"))
		return
	}
	if !prov.Enabled {
		fail(c, http.StatusBadRequest, errors.New("provider disabled"))
		return
	}
	if !h.secrets.Enabled() {
		fail(c, http.StatusBadRequest, errors.New("secret master key not configured (set FERRY_SECRET_KEY)"))
		return
	}
	apiKey, err := provision.DecryptKey(prov, h.secrets)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in provisionRunInput
	_ = c.ShouldBindJSON(&in) // name 可选
	name := in.Name
	if name == "" {
		name = tpl.Name
	}

	m := h.getProvisionManager()
	job := storage.ProvisionJob{TemplateID: tpl.ID, TemplateName: tpl.Name, Action: action, Status: "running"}
	if err := h.db.Create(&job).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// 后台执行并回填这条留痕；单实例不并发由 Manager 全局锁保证。
	go func(jobID uint) {
		status, log := m.Execute(context.Background(), tpl, prov.Type, apiKey, name, action)
		now := time.Now()
		h.db.Model(&storage.ProvisionJob{}).Where("id = ?", jobID).
			Updates(map[string]any{"status": status, "log": log, "finished_at": now})
	}(job.ID)
	c.JSON(http.StatusOK, job)
}

func (h *Handler) applyTemplate(c *gin.Context) { h.runProvision(c, "apply") }
func (h *Handler) planTemplate(c *gin.Context) { h.runProvision(c, "plan") }

// listProvisionJobs 返回供给执行留痕（新在前，默认 50 条）。
func (h *Handler) listProvisionJobs(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	out := []storage.ProvisionJob{}
	if err := h.db.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
