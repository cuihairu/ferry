package handler

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// RSS announcement feed (touch batch TOUCH-2, "User Touch Design" §1): GET /feed.xml is a public subscription,
// sourced from the site-level anchor rows (user_id=0) written when an announcement fans out, so past announcements stay
// visible as a disconnected-ops fallback. No credential (RSS readers cannot carry a token), and the content is announcements only — no user-scoped data at all.

// feedItems is the cap on announcements per output.
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

// feedXML renders the announcement RSS (GET /feed.xml). Window rule (P2-5): announcements that have not come due and not
// fanned out (starts_at in the future, fanout_at empty) and those already withdrawn (ends_at in the past) are not shown;
// legacy rows with all three fields empty are unaffected; a scheduled announcement already fanned out still shows once due.
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
