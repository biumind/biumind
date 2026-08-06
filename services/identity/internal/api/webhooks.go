// webhooks.go — W5-9 微信支付 / 支付宝异步通知 webhook 路由.
//
// 公开 endpoint (没有 Bearer token, 但有各家自己的签名 / AES-GCM 验证):
//
//   POST /v1/billing/wechat/callback   — 微信支付 v3 通知
//   POST /v1/billing/alipay/callback   — 支付宝异步通知 (form 表单)
//
// 流程统一:
//   1. 验签 (微信 RSA + 5min 时间窗 + AES-GCM 解密 / 支付宝 RSA2 form 验签)
//   2. 解析 out_trade_no + 业务事件类型
//   3. UPDATE billing.payment_orders 状态
//   4. ACK 200 给上游
//
// 不做 subscription 状态机推进 — 那部分由后台对账 job 在看到 paid order
// 后兑现 (Create/Activate). 简化此处职责让 webhook 即使 subscription 路径
// 故障也能稳定接收支付通知, 不丢钱.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/identity/internal/billing"
)

// ─── 微信回调 ──────────────────────────────────────────

func (s *Server) handleWechatCallback(w http.ResponseWriter, r *http.Request) {
	if s.Wechat == nil || !s.Wechat.Enabled() {
		writeWechatAck(w, http.StatusServiceUnavailable, "FAIL", "wechat not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeWechatAck(w, http.StatusBadRequest, "FAIL", "bad body")
		return
	}
	tx, err := s.Wechat.VerifyAndDecodeCallback(r.Header, body)
	if err != nil {
		s.logger().Warn("wechat webhook verify", "err", err.Error())
		writeWechatAck(w, http.StatusUnauthorized, "FAIL", err.Error())
		return
	}

	// 路由按 trade_state. SUCCESS = 支付成功; REFUND = 退款 (来自 refund 通知).
	pool := s.Subscriptions.Pool()
	switch tx.TradeState {
	case "SUCCESS":
		if err := markOrderPaid(r.Context(), pool, tx.OutTradeNo, tx.TransactionID); err != nil {
			s.logger().Warn("wechat webhook: mark paid failed",
				"out_trade_no", tx.OutTradeNo, "err", err.Error())
		} else {
			// 兑现 topup 订单 (subscription 走另一路径)
			if err := s.fulfillTopupOrder(r.Context(), tx.OutTradeNo); err != nil {
				s.logger().Warn("wechat webhook: topup fulfill failed",
					"out_trade_no", tx.OutTradeNo, "err", err.Error())
			}
		}
	case "REFUND":
		if err := markOrderRefunded(r.Context(), pool, tx.OutTradeNo); err != nil {
			s.logger().Warn("wechat webhook: mark refund failed",
				"out_trade_no", tx.OutTradeNo, "err", err.Error())
		}
	default:
		// NOTPAY / CLOSED / REVOKED / USERPAYING / PAYERROR — 记 audit 不动状态.
		s.logger().Info("wechat webhook: non-terminal state",
			"out_trade_no", tx.OutTradeNo, "state", tx.TradeState)
	}
	writeWechatAck(w, http.StatusOK, "SUCCESS", "")
}

// writeWechatAck — 微信要求 application/json {code, message}.
func writeWechatAck(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}

// ─── 支付宝回调 ────────────────────────────────────────

func (s *Server) handleAlipayCallback(w http.ResponseWriter, r *http.Request) {
	if s.Alipay == nil || !s.Alipay.Enabled() {
		// 支付宝异步通知期望返回 "fail" / "success" 纯文本.
		_, _ = io.WriteString(w, "fail")
		return
	}
	if err := r.ParseForm(); err != nil {
		_, _ = io.WriteString(w, "fail")
		return
	}
	form := r.PostForm
	if len(form) == 0 {
		// fallback: form values 可能在 query (一些 SDK 行为)
		form = r.URL.Query()
	}
	if err := s.Alipay.VerifyCallback(form); err != nil {
		s.logger().Warn("alipay webhook verify", "err", err.Error())
		_, _ = io.WriteString(w, "fail")
		return
	}
	outTradeNo := form.Get("out_trade_no")
	tradeStatus := form.Get("trade_status")
	if outTradeNo == "" {
		_, _ = io.WriteString(w, "fail")
		return
	}

	pool := s.Subscriptions.Pool()
	switch tradeStatus {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		if err := markOrderPaid(r.Context(), pool, outTradeNo, form.Get("trade_no")); err != nil {
			s.logger().Warn("alipay webhook: mark paid", "out_trade_no", outTradeNo, "err", err.Error())
		} else {
			if err := s.fulfillTopupOrder(r.Context(), outTradeNo); err != nil {
				s.logger().Warn("alipay webhook: topup fulfill failed",
					"out_trade_no", outTradeNo, "err", err.Error())
			}
		}
	case "TRADE_CLOSED":
		if err := markOrderClosed(r.Context(), pool, outTradeNo, form.Get("close_reason")); err != nil {
			s.logger().Warn("alipay webhook: mark closed", "out_trade_no", outTradeNo, "err", err.Error())
		}
	default:
		s.logger().Info("alipay webhook: unknown trade_status",
			"out_trade_no", outTradeNo, "trade_status", tradeStatus)
	}
	_, _ = io.WriteString(w, "success")
}

// ─── payment_orders 状态机 ──────────────────────────────

func markOrderPaid(ctx context.Context, pool *pgxpool.Pool, outTradeNo, providerEventID string) error {
	if outTradeNo == "" {
		return errors.New("out_trade_no empty")
	}
	cmd, err := pool.Exec(ctx, `
		UPDATE billing.payment_orders
		SET status = 'succeeded',
		    provider_event_id = COALESCE($2, provider_event_id),
		    paid_at = COALESCE(paid_at, now()),
		    updated_at = now()
		WHERE provider_order_id = $1
		  AND status IN ('pending', 'failed')
	`, outTradeNo, nullableStr(providerEventID))
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errOrderNotFound
	}
	return nil
}

func markOrderRefunded(ctx context.Context, pool *pgxpool.Pool, outTradeNo string) error {
	cmd, err := pool.Exec(ctx, `
		UPDATE billing.payment_orders
		SET status = 'refunded',
		    refunded_at = COALESCE(refunded_at, now()),
		    updated_at = now()
		WHERE provider_order_id = $1
	`, outTradeNo)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errOrderNotFound
	}
	return nil
}

func markOrderClosed(ctx context.Context, pool *pgxpool.Pool, outTradeNo, reason string) error {
	cmd, err := pool.Exec(ctx, `
		UPDATE billing.payment_orders
		SET status = 'canceled',
		    failure_message = COALESCE(NULLIF($2,''), failure_message),
		    updated_at = now()
		WHERE provider_order_id = $1
		  AND status IN ('pending', 'failed')
	`, outTradeNo, reason)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errOrderNotFound
	}
	return nil
}

var errOrderNotFound = errors.New("payment_orders: not found by provider_order_id")

// ─── helpers ───────────────────────────────────────────

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// logger — Server.Logger 可能 nil (老测试 setup 没注入), 走 slog.Default 兜底.
func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// _ — 避免 import url 未用的兜底; alipay 走 r.ParseForm 内部用 url.Values.
var _ = url.Values{}
var _ = strings.HasPrefix
var _ = time.Now
var _ = billing.WechatTransaction{}
