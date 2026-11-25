package pool

import (
	"errors"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Standby list (BR-3): entries with pool_state=standby are "registered standby machines" —
// not in subscriptions and never auto-resumed (health does not migrate them); a sick probe still
// removes them from the list. Backfill promotes them via recovery pipeline L2 (standby_promote); admins can mark/return them manually.

// StateStandby is the standby state (between active and suspended).
const StateStandby = "standby"

// ErrNotActive is returned when marking standby on a node not in the pool (suspended/already standby).
var ErrNotActive = errors.New("node not active in pool")

// ErrNotStandby is returned when returning a standby node to the pool while it is not in standby.
var ErrNotStandby = errors.New("node not in standby")

// MarkStandby marks standby (admin side): an in-pool entry goes on standby — out of subscriptions
// but still online; not in the pool (suspended/already standby) reports ErrNotActive.
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

// StandbyEntry picks one standby machine usable for backfill (an online standby entry, head first
// for id rotation); returns the zero row when there is none.
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

// Promote lifts a standby machine into the pool (L2 backfill): sets active with a trace; subscriptions switch lines next round.
func Promote(db *gorm.DB, nodeID uint, reason string) error {
	if reason == "" {
		reason = "预备补位"
	}
	return setPoolState(db, nodeID, StateActive, reason)
}
