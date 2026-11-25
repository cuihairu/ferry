// Package keyinit is the signing/encryption key initialization gate (FERRY-003): three service
// secrets (FERRY_ADMIN_SECRET for the admin JWT signature, FERRY_API_SECRET for the API Token
// signature, FERRY_SECRET_KEY the master key for secret encryption) are resolved in the order
// "explicit config > key file > generate and persist". Goal: a fresh install gets random keys
// on first start and stays stable across restarts; production (FERRY_ENV=production) refuses to
// boot when a key cannot be persisted safely, is weak, or is set to the dev default constants explicitly - the legacy dev defaults of apitoken.go are no longer swallowed silently. Key values only ever reach cfg/the key file; errors and logs never echo them.
package keyinit

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cuihairu/ferry/server/internal/config"
)

// keyfile is the default key-file name (next to the sqlite DB, i.e. the /data volume in a container, kept across restarts).
const defaultKeyFileName = "ferry-keys.env"

// devFallbackSecrets are the legacy dev default constants (matching the apitoken.go fallback):
// configuring them explicitly in production is a configuration accident, so boot is refused.
var devFallbackSecrets = map[string]string{
	"FERRY_ADMIN_SECRET": "ferry-admin-secret",
	"FERRY_API_SECRET":   "ferry-api-secret",
}

// keySpec describes one secret: env name, cfg value, dev default constant.
type keySpec struct {
	envName    string
	value      string
	devDefault string
}

// Result reports the resolution: Generated = env names newly created this round;
// KeyFile = the persistence file ("" = not written); Warnings = non-fatal warnings (weak key etc., visible in dev).
type Result struct {
	Generated []string
	KeyFile   string
	Warnings  []string
}

// Resolve determines all three secrets per the gate rules and returns the updated cfg. Any
// step that cannot be resolved safely in production errors out (the main side exits fatally); in dev it falls back to the original cfg values and records Warnings.
func Resolve(cfg config.Config) (config.Config, Result, error) {
	res := Result{}
	production := cfg.Env == "production"

	kf, err := keyFilePath(cfg)
	if err != nil {
		return cfg, res, err
	}
	res.KeyFile = kf

	fileVals := map[string]string{}
	if kf != "" {
		if v, err := readKeyFile(kf); err == nil {
			fileVals = v
		} else if !os.IsNotExist(err) {
			return cfg, res, fmt.Errorf("read key file %s: %w", kf, err)
		}
	}

	specs := []keySpec{
		{"FERRY_ADMIN_SECRET", cfg.AdminSecret, devFallbackSecrets["FERRY_ADMIN_SECRET"]},
		{"FERRY_API_SECRET", cfg.ApiTokenSecret, devFallbackSecrets["FERRY_API_SECRET"]},
		{"FERRY_SECRET_KEY", cfg.SecretKey, ""},
	}
	generated := map[string]string{}
	for i := range specs {
		sp := &specs[i]
		switch {
		case sp.value != "":
			// Explicitly configured (Load already injected it from the env): validate and use as is, never persisted.
			if err := checkKey(sp.envName, sp.value, production); err != nil {
				return cfg, res, err
			}
		case fileVals[sp.envName] != "":
			// Already in the key file: load and use it, no regeneration (stable across restarts).
			sp.value = fileVals[sp.envName]
			if err := checkKey(sp.envName, sp.value, production); err != nil {
				return cfg, res, err
			}
		default:
			// Neither: generate a random key.
			v, err := generateKey()
			if err != nil {
				return cfg, res, fmt.Errorf("generate %s: %w", sp.envName, err)
			}
			sp.value = v
			generated[sp.envName] = v
		}
	}

	// Persist the newly generated keys; if that write fails, production refuses while dev degrades
	// to unpersisted (falls back to the original cfg values with a warning, keeping the legacy dev behaviour).
	if len(generated) > 0 {
		if kf == "" {
			if production {
				return cfg, res, errors.New("production has no key file configured (FERRY_KEYFILE) and no sqlite data dir to persist generated keys")
			}
			res.Warnings = append(res.Warnings,
				"keys generated but no key file available (FERRY_KEYFILE unset, non-sqlite db); "+
					"they are ephemeral this run — configure FERRY_ADMIN_SECRET/FERRY_API_SECRET/FERRY_SECRET_KEY explicitly")
			res.Generated = sortedNames(generated)
			return cfg, res, nil // dev environment: cfg keeps its original values (downstream swallows the legacy dev defaults)
		}
		if err := mergeKeyFile(kf, fileVals, generated); err != nil {
			if production {
				return cfg, res, fmt.Errorf("persist generated keys to %s: %w", kf, err)
			}
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("could not persist generated keys to %s (%v); they are ephemeral this run", kf, err))
			res.Generated = sortedNames(generated)
			return cfg, res, nil
		}
		for _, name := range sortedNames(generated) {
			res.Generated = append(res.Generated, name)
		}
	}

	// A weak key only warns in dev (production already refused it in checkKey).
	for i := range specs {
		sp := &specs[i]
		if sp.value != "" && len(sp.value) < 16 {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s is shorter than 16 chars; use a longer random value", sp.envName))
		}
	}

	cfg.AdminSecret = specs[0].value
	cfg.ApiTokenSecret = specs[1].value
	cfg.SecretKey = specs[2].value
	return cfg, res, nil
}

// keyFilePath decides the key-file path: an explicit FERRY_KEYFILE is used verbatim (its parent
// directory must already exist); when unset, ferry-keys.env next to the sqlite DB (created with the DB directory);
// a non-sqlite DB with no config means nowhere to persist, so it returns an empty string for the caller to judge.
func keyFilePath(cfg config.Config) (string, error) {
	if cfg.KeyFile != "" {
		if filepath.Dir(cfg.KeyFile) == cfg.KeyFile {
			return "", fmt.Errorf("invalid FERRY_KEYFILE %q", cfg.KeyFile)
		}
		return cfg.KeyFile, nil
	}
	if cfg.DBDriver != "sqlite" || cfg.DBDSN == "" || cfg.DBDSN == ":memory:" {
		return "", nil
	}
	dir := filepath.Dir(cfg.DBDSN)
	// The DB file and the key file share a directory (the data volume inside a container); if the
	// directory does not exist it is created together, per the same rule as storage.Open creating the DB directory.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create key file dir %s: %w", dir, err)
	}
	return filepath.Join(dir, defaultKeyFileName), nil
}

// checkKey validates one key: production refuses the dev default constants and short keys (<16);
// dev only records a short key in Result.Warnings (returns nil here) and still uses the weak constant (good enough to run locally).
func checkKey(envName, value string, production bool) error {
	if !production {
		return nil
	}
	if dev, ok := devFallbackSecrets[envName]; ok && value == dev {
		return fmt.Errorf("%s is set to the well-known development default in production; generate a random value", envName)
	}
	if len(value) < 16 {
		return fmt.Errorf("%s is shorter than 16 chars in production; generate a longer random value", envName)
	}
	return nil
}

// generateKey creates a 256-bit random key (base64url), as strong as handler.RandomToken but
// for a different purpose (a service-side signing/encryption key, never mixed with node/subscription tokens).
func generateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// readKeyFile parses KEY=VALUE lines (# comments and blank lines skipped) into env name -> value.
func readKeyFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// mergeKeyFile keeps the existing content verbatim and only appends the newly generated
// KEY=value lines of this round (explicitly configured values are never written), file mode 0600.
func mergeKeyFile(path string, existing, generated map[string]string) error {
	var b strings.Builder
	if len(existing) == 0 {
		b.WriteString("# ferry 服务密钥（FERRY-003）：首启自动生成，删除本文件将使已签发\n")
		b.WriteString("# 的管理员 JWT/API Token 失效、已加密机密不可解密——迁移请整体拷走。\n")
	}
	for _, name := range sortedNames(generated) {
		fmt.Fprintf(&b, "%s=%s\n", name, generated[name])
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}

// sortedNames emits the env names in a stable order (ADMIN, API, SECRET_KEY) so the persisted line order is reproducible.
func sortedNames(m map[string]string) []string {
	order := []string{"FERRY_ADMIN_SECRET", "FERRY_API_SECRET", "FERRY_SECRET_KEY"}
	out := make([]string, 0, len(order))
	for _, o := range order {
		if _, ok := m[o]; ok {
			out = append(out, o)
		}
	}
	return out
}
