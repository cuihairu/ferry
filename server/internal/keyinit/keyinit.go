// Package keyinit 是签名/加密密钥的初始化门禁（FERRY-003）：三把服务密钥
// （管理员 JWT 签名 FERRY_ADMIN_SECRET、API Token 签名 FERRY_API_SECRET、
// 机密加密主密钥 FERRY_SECRET_KEY）按「显式配置 > 密钥文件 > 生成并持久化」
// 定值。目的：新装首启自动拿随机钥且重启稳定；生产环境（FERRY_ENV=production）
// 缺钥无从持久化、弱钥、显式配开发默认常量一律拒绝启动——不再静默吃
// apitoken.go 的历史开发缺省。密钥值只进 cfg/密钥文件，错误与日志一律不回显。
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

// keyfile 默认文件名（落 sqlite 库同目录，容器里即 /data 卷，重启随卷保留）。
const defaultKeyFileName = "ferry-keys.env"

// devFallbackSecrets 是历史开发缺省常量（与 apitoken.go 回退一致）：生产环境
// 显式配成它们=配置事故，拒绝启动。
var devFallbackSecrets = map[string]string{
	"FERRY_ADMIN_SECRET": "ferry-admin-secret",
	"FERRY_API_SECRET":   "ferry-api-secret",
}

// keySpec 描述一把密钥：env 名、cfg 取值、dev 缺省常量。
type keySpec struct {
	envName    string
	value      string
	devDefault string
}

// Result 报告解析结果：Generated=本轮新生成的 env 名；KeyFile=持久化文件
// （""=未落盘）；Warnings=非致命告警（弱钥等，开发环境可见）。
type Result struct {
	Generated []string
	KeyFile   string
	Warnings  []string
}

// Resolve 按门禁口径定值三把密钥，返回更新后的 cfg。生产环境任何一步无法
// 安全定值即报错（main 侧 Fatal 退出）；开发环境回退 cfg 原值并记 Warnings。
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
			// 显式配置（Load 已从 env 注入）：校验后直接用，不落盘。
			if err := checkKey(sp.envName, sp.value, production); err != nil {
				return cfg, res, err
			}
		case fileVals[sp.envName] != "":
			// 密钥文件已有：加载使用，不重生成（重启稳定）。
			sp.value = fileVals[sp.envName]
			if err := checkKey(sp.envName, sp.value, production); err != nil {
				return cfg, res, err
			}
		default:
			// 都没有：生成随机钥。
			v, err := generateKey()
			if err != nil {
				return cfg, res, fmt.Errorf("generate %s: %w", sp.envName, err)
			}
			sp.value = v
			generated[sp.envName] = v
		}
	}

	// 新生成的钥持久化；写不进去时生产拒绝、开发降级为不持久化（回退 cfg
	// 原值并告警，维持历史开发行为）。
	if len(generated) > 0 {
		if kf == "" {
			if production {
				return cfg, res, errors.New("production has no key file configured (FERRY_KEYFILE) and no sqlite data dir to persist generated keys")
			}
			res.Warnings = append(res.Warnings,
				"keys generated but no key file available (FERRY_KEYFILE unset, non-sqlite db); "+
					"they are ephemeral this run — configure FERRY_ADMIN_SECRET/FERRY_API_SECRET/FERRY_SECRET_KEY explicitly")
			res.Generated = sortedNames(generated)
			return cfg, res, nil // 开发环境：cfg 保持原值（下游吃历史开发缺省）
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

	// 弱钥开发环境只告警（生产已在 checkKey 拒绝）。
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

// keyFilePath 定密钥文件路径：显式 FERRY_KEYFILE 原样用（父目录须已存在）；
// 未配置时 sqlite 库同目录 ferry-keys.env（目录随库建）；非 sqlite 未配置=
// 无处持久化，返回空串交上层判定。
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
	// 库文件目录与密钥文件同处（容器内即数据卷）；目录不存在则一并创建，
	// 与 storage.Open 建库目录同口径。
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create key file dir %s: %w", dir, err)
	}
	return filepath.Join(dir, defaultKeyFileName), nil
}

// checkKey 单钥校验：生产环境拒开发缺省常量与短钥（<16）；开发环境短钥
// 只在 Result.Warnings 记（此处返回 nil），弱常量照用（本地跑通用）。
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

// generateKey 生成 256 位随机钥（base64url），与 handler.RandomToken 同强度
// 不同用途（此处是服务侧签名/加密钥，不与节点/订阅令牌混用）。
func generateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// readKeyFile 解析 KEY=VALUE 行（# 注释与空行跳过），返回 env 名→值。
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

// mergeKeyFile 既有内容原样保留，只追加本轮新生成的 KEY=value 行（显式配置
// 的值绝不落盘），文件 0600。
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

// sortedNames 稳定输出 env 名顺序（ADMIN、API、SECRET_KEY，落盘行序可复现）。
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
