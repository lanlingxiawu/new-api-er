package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// topup_export.go: filter parsing (with anti-privilege-escalation for non-admins),
// CSV row/field building (formula-injection guard), time formatting, and the
// per-user export rate-limit slot acquisition.

func TestParseTopUpListFilter_NonAdminForcesSelfAndWindow(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x?user_id=999&status=success", nil)
	ctx.Set("id", 777)

	f, err := parseTopUpListFilter(ctx, false)
	require.NoError(t, err)
	// Client-supplied user_id=999 must be ignored; locked to caller 777.
	assert.Equal(t, 777, f.UserId)
	assert.True(t, f.EnforceWindow)
	assert.Equal(t, common.TopUpStatusSuccess, f.Status)
}

func TestParseTopUpListFilter_NonAdminUnauthenticated(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x", nil)
	// no id in context -> GetInt returns 0 -> unauthorized
	_, err := parseTopUpListFilter(ctx, false)
	assert.Error(t, err)
}

func TestParseTopUpListFilter_AdminHonorsUserId(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x?user_id=555", nil)
	ctx.Set("id", 1)
	f, err := parseTopUpListFilter(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, 555, f.UserId)
	assert.False(t, f.EnforceWindow)
}

func TestParseTopUpListFilter_RejectsBadStatusAndPaymentMethod(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x?status=hacked", nil)
	ctx.Set("id", 1)
	_, err := parseTopUpListFilter(ctx, true)
	assert.Error(t, err, "non-whitelisted status must be rejected")

	ctx2, _ := newCtx(t, "GET", "/x?payment_method=bad-method", nil)
	ctx2.Set("id", 1)
	_, err = parseTopUpListFilter(ctx2, true)
	assert.Error(t, err, "payment_method with unsafe chars must be rejected")

	// Valid payment method passes the pattern.
	ctx3, _ := newCtx(t, "GET", "/x?payment_method=wxpay_01", nil)
	ctx3.Set("id", 1)
	f, err := parseTopUpListFilter(ctx3, true)
	require.NoError(t, err)
	assert.Equal(t, "wxpay_01", f.PaymentMethod)
}

func TestValidTopUpStatusesWhitelist(t *testing.T) {
	assert.True(t, validTopUpStatuses[common.TopUpStatusPending])
	assert.True(t, validTopUpStatuses[common.TopUpStatusSuccess])
	assert.True(t, validTopUpStatuses[common.TopUpStatusFailed])
	assert.True(t, validTopUpStatuses[common.TopUpStatusExpired])
	assert.False(t, validTopUpStatuses["arbitrary"])
}

func TestPaymentMethodPattern(t *testing.T) {
	assert.True(t, paymentMethodPattern.MatchString("alipay"))
	assert.True(t, paymentMethodPattern.MatchString("wechat_pay_2"))
	assert.False(t, paymentMethodPattern.MatchString("bad-method")) // hyphen not allowed
	assert.False(t, paymentMethodPattern.MatchString(""))
	assert.False(t, paymentMethodPattern.MatchString("a b"))
}

func TestCsvSafeField(t *testing.T) {
	// Formula-injection leading chars get a single-quote prefix.
	assert.Equal(t, "'=cmd", csvSafeField("=cmd"))
	assert.Equal(t, "'+1", csvSafeField("+1"))
	assert.Equal(t, "'-1", csvSafeField("-1"))
	assert.Equal(t, "'@x", csvSafeField("@x"))
	assert.Equal(t, "'\tx", csvSafeField("\tx"))
	// Safe values pass through.
	assert.Equal(t, "normal", csvSafeField("normal"))
	assert.Equal(t, "", csvSafeField(""))
}

func TestFormatExportTime(t *testing.T) {
	assert.Equal(t, "", formatExportTime(0))
	assert.Equal(t, "", formatExportTime(-1))
	// Non-zero renders a full timestamp (length check avoids TZ coupling).
	assert.Len(t, formatExportTime(1_600_000_000), len("2006-01-02 15:04:05"))
}

func TestTopUpExportRow(t *testing.T) {
	rec := &model.TopUp{
		Id:              1,
		UserId:          88,
		TradeNo:         "=INJECT", // must be escaped
		PaymentMethod:   "alipay",
		Amount:          10,
		Money:           3.5,
		PaymentCurrency: "CNY",
		Status:          common.TopUpStatusSuccess,
		CreateTime:      0,
		CompleteTime:    0,
	}
	// Without user id column.
	row := topUpExportRow(rec, false)
	assert.Equal(t, "'=INJECT", row[0])
	assert.Equal(t, "alipay", row[1])
	assert.Equal(t, "10", row[2])
	assert.Equal(t, "3.50", row[3])
	assert.Equal(t, "CNY", row[4])
	assert.Equal(t, common.TopUpStatusSuccess, row[5])
	assert.Equal(t, "", row[6]) // zero create time
	assert.Equal(t, "", row[7]) // zero complete time

	// With user id column (admin export) inserts UserId as column 2.
	rowAdmin := topUpExportRow(rec, true)
	assert.Equal(t, "88", rowAdmin[1])
	assert.Len(t, rowAdmin, len(row)+1)
}

func TestTopUpExportColumnHeaders(t *testing.T) {
	ctx, _ := newCtx(t, "GET", "/x", nil)
	base := topUpExportColumnHeaders(ctx, false)
	withUser := topUpExportColumnHeaders(ctx, true)
	assert.Len(t, withUser, len(base)+1, "admin headers include the user-id column")
	assert.NotEmpty(t, base[0])
}

func TestAcquireTopUpExportSlot_DisabledEnvAlwaysAllows(t *testing.T) {
	prev, had := os.LookupEnv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
	os.Setenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE", "false")
	t.Cleanup(func() {
		if had {
			os.Setenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE", prev)
		} else {
			os.Unsetenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
		}
	})
	ctx, _ := newCtx(t, "GET", "/x", nil)
	assert.True(t, acquireTopUpExportSlot(ctx, 12345))
	assert.True(t, acquireTopUpExportSlot(ctx, 0)) // even userID<=0 when disabled
}

func TestAcquireTopUpExportSlot_InvalidUserRejected(t *testing.T) {
	// Enabled (default) + no Redis -> falls to memory limiter, but userID<=0
	// is rejected before that.
	prev, had := os.LookupEnv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
	os.Unsetenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
	t.Cleanup(func() {
		if had {
			os.Setenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE", prev)
		}
	})
	ctx, _ := newCtx(t, "GET", "/x", nil)
	assert.False(t, acquireTopUpExportSlot(ctx, 0))
	assert.False(t, acquireTopUpExportSlot(ctx, -3))
}

func TestExportAllTopUps_EmptyResultStreamsCSV(t *testing.T) {
	requireDB(t)
	// Narrow to a non-existent user id so the export contains only the header,
	// exercising generate -> stream without touching any payment gateway.
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	ctx, rec := newCtx(t, "GET", "/api/topup/export?user_id=2000000001", nil)
	ctx.Set("id", nextTestID()) // fresh id -> rate-limit slot granted once
	ExportAllTopUps(ctx)

	assert.Equal(t, 200, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	// UTF-8 BOM prefix then a header row.
	assert.True(t, len(rec.Body.Bytes()) >= 3)
}

func TestAcquireTopUpExportSlot_MemoryLimiterCooldown(t *testing.T) {
	// Enabled + Redis disabled: memory limiter allows the first call for a
	// fresh user id and rejects the immediate second within the cooldown window.
	prev, had := os.LookupEnv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
	os.Unsetenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE")
	t.Cleanup(func() {
		if had {
			os.Setenv("TOPUP_EXPORT_RATE_LIMIT_ENABLE", prev)
		}
	})
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })

	ctx, _ := newCtx(t, "GET", "/x", nil)
	uid := nextTestID()
	assert.True(t, acquireTopUpExportSlot(ctx, uid), "first call allowed")
	assert.False(t, acquireTopUpExportSlot(ctx, uid), "second call within cooldown rejected")
}
