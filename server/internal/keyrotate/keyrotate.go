// Package keyrotate re-encrypts DB-sealed secrets to the store's current key
// version (F-1723). The operator flow is two boots:
//
//  1. Set the master key to the transition keyring (order irrelevant), e.g.
//     FERRY_SECRET_KEY="v1:<old>,v2:<new>": this walk re-encrypts every sealed
//     value to v2 while v1 still decrypts, so nothing is lost mid-flight.
//  2. Retire the old term (FERRY_SECRET_KEY="v2:<new>") and restart: only the
//     new key remains; anything the walk missed surfaces as a decrypt error.
//
// The walk covers every column the seal surface writes (providerapi.sealSecret
// callers): providers.access_key, dns_providers.api_key and users.totp_secret.
package keyrotate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Run re-encrypts every sealed DB value below the store's current version and
// returns the count. It is idempotent: a second run over migrated rows is 0.
// A value that cannot be decrypted under the supplied keyring (an old-version
// key missing from it, or a corrupt row) is skipped and reported — the row
// keeps its old ciphertext, so re-adding the key term and re-running fixes it;
// a failed walk never fails the boot.
func Run(db *gorm.DB, store *secret.Store) (int, error) {
	if !store.Enabled() || store.CurrentVersion() <= secret.Version {
		return 0, nil // single current-version keyring: nothing to migrate
	}
	cur := store.CurrentVersion()
	n := 0
	var firstErr error
	visit := func(model any, column string) {
		var rows []struct {
			ID    uint
			Value string
		}
		if err := db.Model(model).Where(column + " != ''").
			Select("id, " + column + " AS value").Find(&rows).Error; err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("scan %s: %w", column, err)
			}
			return
		}
		for _, r := range rows {
			ver := cipherVersion(r.Value)
			if ver == 0 {
				continue // not our ciphertext (legacy plaintext or junk): untouched
			}
			if ver >= cur {
				continue // already current
			}
			plain, err := store.Decrypt(r.Value)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s id=%d: %w", column, r.ID, err)
				}
				continue
			}
			sealed, err := store.Encrypt(plain)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s id=%d: %w", column, r.ID, err)
				}
				continue
			}
			if err := db.Model(model).Where("id = ?", r.ID).
				Update(column, sealed).Error; err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s id=%d: %w", column, r.ID, err)
			}
			n++
		}
	}
	visit(&storage.Provider{}, "access_key")
	visit(&storage.DNSProvider{}, "api_key")
	visit(&storage.User{}, "totp_secret")
	return n, firstErr
}

// cipherVersion reads the "v<n>:" header; 0 for a malformed/legacy value.
func cipherVersion(ct string) int {
	parts := strings.SplitN(ct, ":", 2)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "v") {
		return 0
	}
	n, err := strconv.Atoi(parts[0][1:])
	if err != nil {
		return 0
	}
	return n
}
