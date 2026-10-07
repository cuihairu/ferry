// Package epusdt 对接开源 epusdt 系 USDT(TRC20) 收款网关（PAY-8，
// 协议口径：assimon/epusdt）。下单走 create-transaction 拿收银台地址，
// 异步回调按「参数名 ASCII 升序 k=v& 拼接 + api_token 后取 md5」验签，
// status=2 视为支付成功。
//
// 配置（环境变量，未配置时 Provider 返回未启用错误）：
//
//	FERRY_EPUSDT_URL        网关基址，如 https://pay.example.com
//	FERRY_EPUSDT_TOKEN      网关 api_token（下单签名与回调验签共用）
//	FERRY_EPUSDT_NOTIFY_URL 回调通知地址（须对外可达，如 https://panel.example.com/api/pay/epusdt/notify）
package epusdt

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
)

// 环境变量名与协议常量。
const (
	EnvURL       = "FERRY_EPUSDT_URL"
	EnvToken     = "FERRY_EPUSDT_TOKEN"
	EnvNotifyURL = "FERRY_EPUSDT_NOTIFY_URL"
	createPath   = "/api/v1/order/create-transaction"
	statusPaid   = "2" // 回调 status=2 支付成功（epusdt 订单状态口径）
	httpTimeout  = 10 * time.Second
)

// Epusdt 是 epusdt 网关的 payment.Provider 实现。
type Epusdt struct {
	baseURL   string // 网关基址，无尾斜杠
	token     string
	notifyURL string
	client    *http.Client
}

// 编译期契约校验。
var _ payment.Provider = (*Epusdt)(nil)

// NewFromEnv 从环境变量装配；缺失项在 CreateOrder/Verify 时报未配置。
func NewFromEnv() *Epusdt {
	return &Epusdt{
		baseURL:   strings.TrimRight(os.Getenv(EnvURL), "/"),
		token:     os.Getenv(EnvToken),
		notifyURL: os.Getenv(EnvNotifyURL),
		client:    &http.Client{Timeout: httpTimeout},
	}
}

func init() { payment.Register(NewFromEnv()) }

// Name 实现 payment.Provider。
func (e *Epusdt) Name() string { return "epusdt" }

func (e *Epusdt) configured() error {
	if e.baseURL == "" || e.token == "" {
		return fmt.Errorf("epusdt 未配置（需 %s / %s）", EnvURL, EnvToken)
	}
	return nil
}

// createResp 是 epusdt 下单响应信封（仅取用到的字段）。
type createResp struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Data       struct {
		OrderID             string `json:"order_id"`
		TradeID             string `json:"trade_id"`
		PaymentURL          string `json:"payment_url"`
		ActualAmount        string `json:"actual_amount"`
		ExpirationTimestamp int64  `json:"expiration_timestamp"`
	} `json:"data"`
}

// CreateOrder 创建收款订单：调 create-transaction 换收银台地址。
// 金额口径：Order.AmountCents 为人民币分，epusdt 按 amount（元，两位小数）
// 自动折算 USDT 并处理汇率波动。
func (e *Epusdt) CreateOrder(ctx context.Context, o payment.Order) (payment.Receipt, error) {
	if err := e.configured(); err != nil {
		return payment.Receipt{}, err
	}
	if o.OrderNo == "" || o.AmountCents <= 0 {
		return payment.Receipt{}, errors.New("epusdt: order_no 与金额必填")
	}
	form := url.Values{
		"amount":     {centsToYuan(o.AmountCents)},
		"order_id":   {o.OrderNo},
		"notify_url": {e.notifyURL},
	}
	form.Set("signature", e.sign(form))

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+createPath, strings.NewReader(form.Encode()))
	if err != nil {
		return payment.Receipt{}, err
	}
	hreq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.client.Do(hreq)
	if err != nil {
		return payment.Receipt{}, fmt.Errorf("epusdt create: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return payment.Receipt{}, err
	}
	var out createResp
	if err := json.Unmarshal(body, &out); err != nil {
		return payment.Receipt{}, fmt.Errorf("epusdt create: 响应非 JSON: %w", err)
	}
	if out.StatusCode != http.StatusOK {
		return payment.Receipt{}, fmt.Errorf("epusdt create: %d %s", out.StatusCode, out.Message)
	}
	if out.Data.PaymentURL == "" || out.Data.TradeID == "" {
		return payment.Receipt{}, errors.New("epusdt create: 缺 payment_url/trade_id")
	}
	rcpt := payment.Receipt{Provider: e.Name(), PayURL: out.Data.PaymentURL, ExternalID: out.Data.TradeID}
	if out.Data.ExpirationTimestamp > 0 {
		rcpt.ExpiresAt = time.Unix(out.Data.ExpirationTimestamp, 0)
	}
	return rcpt, nil
}

// Verify 验证异步回调：表单参数重算签名 → status 必须=2 → 汇出结论。
// 回调关键参数：order_id / trade_id / amount（元）/ status / signature。
func (e *Epusdt) Verify(_ context.Context, raw []byte) (payment.Callback, error) {
	if err := e.configured(); err != nil {
		return payment.Callback{}, err
	}
	vals, err := url.ParseQuery(string(raw))
	if err != nil {
		return payment.Callback{}, fmt.Errorf("epusdt verify: 回调非表单: %w", err)
	}
	if sig := vals.Get("signature"); sig == "" || e.sign(vals) != sig {
		return payment.Callback{}, errors.New("epusdt verify: 签名不符")
	}
	if vals.Get("status") != statusPaid {
		return payment.Callback{}, fmt.Errorf("epusdt verify: status=%s 非支付成功", vals.Get("status"))
	}
	cents, err := yuanToCents(vals.Get("amount"))
	if err != nil {
		return payment.Callback{}, fmt.Errorf("epusdt verify: 金额非法 %q", vals.Get("amount"))
	}
	return payment.Callback{
		OrderNo:     vals.Get("order_id"),
		ExternalID:  vals.Get("trade_id"),
		AmountCents: cents,
		PaidAt:      time.Now(),
		Raw:         raw,
	}, nil
}

// sign 按 epusdt 规则签名：参数名 ASCII 升序、空值跳过、k=v& 拼接后
// 直接接 api_token，取 32 位小写 md5（signature 自身不参与）。
func (e *Epusdt) sign(vals url.Values) string {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k == "signature" || vals.Get(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+vals.Get(k))
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + e.token))
	return hex.EncodeToString(sum[:])
}

// centsToYuan 分 → 元字符串（两位小数）。
func centsToYuan(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

// yuanToCents 元字符串 → 分（四舍五入到分位）。
func yuanToCents(yuan string) (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(yuan), 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * 100)), nil
}
