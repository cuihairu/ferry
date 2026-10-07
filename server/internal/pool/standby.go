package pool

import (
	"errors"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 预备清单（BR-3）：pool_state=standby 的入口是「在册待命的备用机」——
// 不入订阅、不被自动判定复位（健康不迁移），探测 sick 照常摘除出清单。
// 补位由恢复流水线 L2 升入池（standby_promote），管理端可手动标记/回池。

// StateStandby 是预备态（介于 active 与 suspended 之间）。
const StateStandby = "standby"

// ErrNotActive 标记预备时节点不在池内（已摘除/已是预备）。
var ErrNotActive = errors.New("node not active in pool")

// ErrNotStandby 预备回池时节点不在预备态。
var ErrNotStandby = errors.New("node not in standby")

// MarkStandby 标记预备（管理端）：在池入口转入待命，出订阅但保持在线；
// 不在池内（已摘除/已是预备）报 ErrNotActive。
func MarkStandby(db *gorm.DB, nodeID uint) (storage.Node, error) {
	var n storage.Node
	if err := db.First(&n, nodeID).Error; err != nil {
		return storage.Node{}, err
	}
	if n.Role != "entry" && n.Role != "both" {
		return storage.Node{}, errors.New("node is not an entry")
	}
	if n.PoolState != StateActive {
		return storage.Node{}, ErrNotActive
	}
	reason := "标记预备"
	if err := setPoolState(db, n.ID, StateStandby, reason); err != nil {
		return storage.Node{}, err
	}
	n.PoolState, n.PoolReason = StateStandby, reason
	return n, nil
}

// StandbyEntry 取一台可补位的预备机（在线的预备入口，按 id 轮换先取头），
// 无则返回空行。
func StandbyEntry(db *gorm.DB) (storage.Node, error) {
	var n storage.Node
	err := db.Where("enabled = ? AND pool_state = ? AND status = ? AND role IN (?, ?)",
		true, StateStandby, "online", "entry", "both").
		Order("id").First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return storage.Node{}, nil
	}
	return n, err
}

// Promote 把预备机升入池（L2 补位）：置 active 并留痕，订阅下轮换线。
func Promote(db *gorm.DB, nodeID uint, reason string) error {
	if reason == "" {
		reason = "预备补位"
	}
	return setPoolState(db, nodeID, StateActive, reason)
}
