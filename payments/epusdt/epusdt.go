// Package epusdt integrates the open-source epusdt family of USDT(TRC20) collection
// gateways (PAY-8, protocol spec: assimon/epusdt). Ordering calls create-transaction to
// get the checkout URL; async callbacks are verified by "parameter names in ASCII
// ascending order joined as k=v& plus api_token, then md5", and status=2 means paid.
//
// Configuration (env vars; when unset the Provider returns a not-enabled error):
//
//	FERRY_EPUSDT_URL        gateway base URL, e.g. https://pay.example.com
//	FERRY_EPUSDT_TOKEN      gateway api_token (shared by order signing and callback verification)
//	FERRY_EPUSDT_NOTIFY_URL callback notify URL (must be publicly reachable, e.g. https://panel.example.com/api/pay/epusdt/notify)
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

// Env var names and protocol constants.
const (
	EnvURL       = "FERRY_EPUSDT_URL"
	EnvToken     = "FERRY_EPUSDT_TOKEN"
	EnvNotifyURL = "FERRY_EPUSDT_NOTIFY_URL"
	createPath   = "/api/v1/order/create-transaction"
	statusPaid   = "2" // callback status=2 means paid (epusdt order state spec)
	httpTimeout  = 10 * time.Second
)

// Epusdt is the payment.Provider implementation for the epusdt gateway.
type Epusdt struct {
	baseURL   string // gateway base URL, no trailing slash
	token     string
	notifyURL string
	client    *http.Client
}

// Compile-time contract check.
var _ payment.Provider = (*Epusdt)(nil)

// NewFromEnv assembles from environment variables; missing pieces error as unconfigured in CreateOrder/Verify.
func NewFromEnv() *Epusdt {
	return &Epusdt{
		baseURL:   strings.TrimRight(os.Getenv(EnvURL), "/"),
		token:     os.Getenv(EnvToken),
		notifyURL: os.Getenv(EnvNotifyURL),
		client:    &http.Client{Timeout: httpTimeout},
	}
}

func init() { payment.Register(NewFromEnv()) }

// Name implements payment.Provider.
func (e *Epusdt) Name() string { return "epusdt" }

func (e *Epusdt) configured() error {
	if e.baseURL == "" || e.token == "" {
		return fmt.Errorf("epusdt 未配置（需 %s / %s）", EnvURL, EnvToken)
	}
	return nil
}

// createResp is the epusdt order response envelope (only the fields used).
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

// CreateOrder creates a collection order: calls create-transaction to get the checkout URL.
// Amount spec: Order.AmountCents is RMB cents; epusdt takes amount (yuan, two decimals)
// and converts to USDT automatically, absorbing exchange-rate movement.
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

// Verify validates an async callback: recompute the signature over the form params → status must equal 2 → emit the verdict.
// Callback key params: order_id / trade_id / amount (yuan) / status / signature.
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

// sign signs per the epusdt rules: parameter names in ASCII ascending order, empty
// values skipped, joined as k=v& then api_token appended directly, taking the 32-char
// lowercase md5 (signature itself does not participate).
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

// centsToYuan converts cents → yuan string (two decimals).
func centsToYuan(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

// yuanToCents converts a yuan string → cents (rounded to the cent).
func yuanToCents(yuan string) (int64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(yuan), 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * 100)), nil
}
