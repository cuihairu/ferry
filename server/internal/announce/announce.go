// Package announce 公告扇出与窗口（P2-5）：管理发布与定时扫描共用一套扇出——
// starts_at 未到点只落站级锚点行（定时发布），到点由 notifyscan 周期检扇出；
// ends_at 到点即撤下（RSS 与 panel 列表过滤，行保留可查）；外通道重发位对
// 已发布公告补一轮 notice 事件（只补站外投递不重落站内信行），限频 6h/公告。
package announce

import (
	"errors"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// resendCooldown 是外通道重发的限频窗口：同一条公告两次重发至少隔 6h。
const resendCooldown = 6 * time.Hour

// 重发被拒的两类原因（handler 侧映射状态码）。
var (
	// ErrCooldown 同公告限频窗口内重复重发。
	ErrCooldown = errors.New("announcement resend cooldown")
	// ErrWindowClosed 公告窗口已结束（撤下语义优先，不再外发）。
	ErrWindowClosed = errors.New("announcement window closed")
)

// Input 是发布一条公告的载荷。
type Input struct {
	Title    string
	Body     string
	StartsAt *time.Time // 定时发布；nil=立即
	EndsAt   *time.Time // 定时撤下；nil=不撤
}

// Fanout 发布一条公告：starts_at 在未来=定时发布（只落锚点行，返回 0，
// 到点由 RunDue 扇出）；否则立即扇出（锚点行+启用用户站内信行+notice 事件
// 外发）。返回实际触达用户数（禁用用户不收）。
func Fanout(db *gorm.DB, in Input) (int64, error) {
	now := time.Now()
	anchor := storage.Notification{
		Type: storage.NotifAnnouncement, Title: in.Title, Body: in.Body,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, CreatedAt: now,
	}
	if in.StartsAt != nil && in.StartsAt.After(now) {
		if err := db.Create(&anchor).Error; err != nil {
			return 0, err
		}
		return 0, nil
	}
	return fanoutUsers(db, &anchor)
}

// RunDue 扇出到点的定时公告（notifyscan 周期调用）：锚点行 starts_at 已到
// 且未扇出。返回本轮新触达用户数。
func RunDue(db *gorm.DB, now time.Time) (int, error) {
	var due []storage.Notification
	if err := db.Where("user_id = ? AND type = ? AND fanout_at IS NULL AND starts_at IS NOT NULL AND starts_at <= ?",
		0, storage.NotifAnnouncement, now).Order("id").Find(&due).Error; err != nil {
		return 0, err
	}
	n := 0
	for i := range due {
		cnt, err := fanoutUsers(db, &due[i])
		if err != nil {
			return n, err
		}
		n += int(cnt)
	}
	return n, nil
}

// Resend 外通道重发（P2-5 ③）：对已发布公告给全部启用用户再落一轮 notice
// 事件（Herald 分发 TG/邮件）——站内信行不重落（用户已有）。限频=同公告
// resendCooldown 内至多一次；窗口已结束的公告不重发。返回本轮外发用户数。
func Resend(db *gorm.DB, anchorID int64, now time.Time) (int64, error) {
	var anchor storage.Notification
	if err := db.Where("id = ? AND user_id = ? AND type = ?", anchorID, 0, storage.NotifAnnouncement).
		First(&anchor).Error; err != nil {
		return 0, err
	}
	if anchor.EndsAt != nil && !anchor.EndsAt.After(now) {
		return 0, ErrWindowClosed
	}
	if anchor.ResendAt != nil && now.Sub(*anchor.ResendAt) < resendCooldown {
		return 0, ErrCooldown
	}
	var ids []int64
	if err := db.Model(&storage.User{}).Where("enabled = ?", true).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	events := make([]storage.Event, 0, len(ids))
	for _, id := range ids {
		events = append(events, storage.Event{
			Kind: herald.KindNotice, Severity: herald.SeverityInfo,
			Title: anchor.Title, Body: anchor.Body,
			Target: herald.TargetUser(id), Status: herald.StatusPending,
			OccurredAt: now, CreatedAt: now,
			DedupKey: resendDedupKey(anchor.ID, now),
		})
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(events, 500).Error; err != nil {
			return 0, err
		}
	}
	if err := db.Model(&anchor).Update("resend_at", now).Error; err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// resendDedupKey 让同公告同轮重发在 Herald 侧窗口合并（轮次按分钟取整）。
func resendDedupKey(anchorID int64, now time.Time) string {
	return "notice_resend:" + strconv.FormatInt(anchorID, 10) + ":" + now.Format("200601021504")
}

// fanoutUsers 给全部启用用户落站内信行并外发 notice 事件；锚点行先落定 ID
//（用户行经 AnchorID 关联，统计发 n/读 m 都按它数）。完成后回写 FanoutAt。
func fanoutUsers(db *gorm.DB, anchor *storage.Notification) (int64, error) {
	var ids []int64
	if err := db.Model(&storage.User{}).Where("enabled = ?", true).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	now := time.Now()
	if anchor.CreatedAt.IsZero() {
		anchor.CreatedAt = now
	}
	// RunDue 路径的锚点行已存在（scheduled 时落过），只在新建路径插入。
	if anchor.ID == 0 {
		if err := db.Create(anchor).Error; err != nil {
			return 0, err
		}
	}
	rows := make([]storage.Notification, 0, len(ids))
	events := make([]storage.Event, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, storage.Notification{
			UserID: id, Type: storage.NotifAnnouncement,
			Title: anchor.Title, Body: anchor.Body,
			AnchorID: anchor.ID, EndsAt: anchor.EndsAt,
			CreatedAt: now,
		})
		events = append(events, storage.Event{
			Kind: herald.KindNotice, Severity: herald.SeverityInfo,
			Title: anchor.Title, Body: anchor.Body,
			Target: herald.TargetUser(id), Status: herald.StatusPending,
			OccurredAt: now, CreatedAt: now,
		})
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		return 0, err
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(events, 500).Error; err != nil {
			return int64(len(ids)), err
		}
	}
	if err := db.Model(anchor).Update("fanout_at", now).Error; err != nil {
		return int64(len(ids)), err
	}
	// 返回用户行数（锚点行不入计数）：扇出语义=触达了多少启用用户。
	return int64(len(ids)), nil
}
