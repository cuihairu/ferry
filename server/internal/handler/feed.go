package handler

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// RSS 公告输出（触达批 TOUCH-2，用户触达设计 §1）：GET /feed.xml 公开订阅，
// 数据源为公告扇出时落的站级锚点行（user_id=0），断联容灾的历史公告可见。
// 无凭据（RSS 阅读器带不了令牌），内容仅公告——不含任何用户维度数据。

// feedItems 是单次输出的公告条数上限。
const feedItems = 50

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

// feedXML 渲染公告 RSS（GET /feed.xml）。窗口口径（P2-5）：未到点且未扇出
// （starts_at 未来、fanout_at 空）与已撤下（ends_at 过去）的公告不出；
// 存量行三字段为空，不受影响；已扇出的定时公告到点后照常出。
func (h *Handler) feedXML(c *gin.Context) {
	now := time.Now()
	var rows []storage.Notification
	if err := h.db.
		Where("user_id = ? AND type = ? AND (fanout_at IS NOT NULL OR starts_at IS NULL OR starts_at <= ?) AND (ends_at IS NULL OR ends_at > ?)",
			0, storage.NotifAnnouncement, now, now).
		Order("id DESC").Limit(feedItems).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]rssItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, rssItem{
			Title:       r.Title,
			Description: r.Body,
			GUID:        strconv.FormatUint(uint64(r.ID), 10),
			PubDate:     r.CreatedAt.UTC().Format(time.RFC1123Z),
		})
	}
	out, err := xml.Marshal(rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       "公告",
			Link:        h.cfg.BaseURL,
			Description: "面板公告订阅",
			Items:       items,
		},
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Data(http.StatusOK, "application/rss+xml; charset=utf-8",
		append([]byte(xml.Header), out...))
}
