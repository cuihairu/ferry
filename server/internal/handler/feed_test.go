package handler

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// TestFeedXML 覆盖公告 RSS（TOUCH-2）：扇出后 /feed.xml 取锚点行渲染
// RSS 2.0、XML 转义合法、无公告输出合法空 feed。
func TestFeedXML(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 发布公告：标题带 XML 特殊字符验转义
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "维护通知 A&B<x>", "body": "周日凌晨升级",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, r, "GET", "/feed.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/rss+xml") {
		t.Fatalf("content type: %s", ct)
	}

	var feed struct {
		Version string `xml:"version,attr"`
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("parse rss: %v\n%s", err, rec.Body)
	}
	if feed.Version != "2.0" || len(feed.Channel.Items) != 1 {
		t.Fatalf("unexpected feed: version=%s items=%d", feed.Version, len(feed.Channel.Items))
	}
	item := feed.Channel.Items[0]
	if item.Title != "维护通知 A&B<x>" || item.Description != "周日凌晨升级" || item.PubDate == "" {
		t.Fatalf("unexpected item: %+v", item)
	}
}

// TestFeedXMLEmpty 空公告时输出合法空 feed（不 500）。
func TestFeedXMLEmpty(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "GET", "/feed.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed empty: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "<rss") {
		t.Fatalf("not rss: %s", rec.Body)
	}
}
