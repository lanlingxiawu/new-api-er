package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Harness: the logger writes to gin.DefaultWriter / DefaultErrorWriter and reads
// currency config from operation_setting. Every test swaps those process globals
// through save/restore helpers so the suite is order-independent.
// ---------------------------------------------------------------------------

// captureWriters redirects both gin writers to buffers for the duration of a
// test and returns (stdout, stderr) buffers.
func captureWriters(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	common.LogWriterMu.Lock()
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = out, errOut
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr
		common.LogWriterMu.Unlock()
	})
	return out, errOut
}

// setDebug toggles common.DebugEnabled with restore.
func setDebug(t *testing.T, v bool) {
	t.Helper()
	orig := common.DebugEnabled
	common.DebugEnabled = v
	t.Cleanup(func() { common.DebugEnabled = orig })
}

// setDisplay mutates the (pointer-shared) general setting and USD rate, with
// full restore.
func setDisplay(t *testing.T, dtype, symbol string, customRate, usdRate float64) {
	t.Helper()
	gs := operation_setting.GetGeneralSetting()
	origType, origSym, origCustom := gs.QuotaDisplayType, gs.CustomCurrencySymbol, gs.CustomCurrencyExchangeRate
	origUSD := operation_setting.USDExchangeRate
	gs.QuotaDisplayType = dtype
	gs.CustomCurrencySymbol = symbol
	gs.CustomCurrencyExchangeRate = customRate
	operation_setting.USDExchangeRate = usdRate
	t.Cleanup(func() {
		gs.QuotaDisplayType = origType
		gs.CustomCurrencySymbol = origSym
		gs.CustomCurrencyExchangeRate = origCustom
		operation_setting.USDExchangeRate = origUSD
	})
}

// ---------------------------------------------------------------------------
// FormatQuota — every switch case + Custom sub-branches (symbol default, rate<=0).
// ---------------------------------------------------------------------------

func TestFormatQuota_USDDefault(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeUSD, "¤", 1, 7.3)
	want := fmt.Sprintf("＄%.6f", 500000.0/common.QuotaPerUnit)
	assert.Equal(t, want, FormatQuota(500000))
}

func TestFormatQuota_CNYUsesUSDExchangeRate(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeCNY, "¤", 1, 7.3)
	usd := 500000.0 / common.QuotaPerUnit
	want := fmt.Sprintf("¥%.6f", usd*7.3)
	assert.Equal(t, want, FormatQuota(500000))
}

func TestFormatQuota_CustomWithSymbolAndRate(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeCustom, "€", 2, 7.3)
	usd := 500000.0 / common.QuotaPerUnit
	want := fmt.Sprintf("€%.6f", usd*2)
	assert.Equal(t, want, FormatQuota(500000))
}

func TestFormatQuota_CustomEmptySymbolFallsBackToGeneric(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeCustom, "", 2, 7.3)
	usd := 500000.0 / common.QuotaPerUnit
	want := fmt.Sprintf("¤%.6f", usd*2)
	assert.Equal(t, want, FormatQuota(500000), "empty symbol => ¤")
}

func TestFormatQuota_CustomNonPositiveRateFallsBackToOne(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeCustom, "€", 0, 7.3)
	usd := 500000.0 / common.QuotaPerUnit
	want := fmt.Sprintf("€%.6f", usd*1) // rate<=0 => 1
	assert.Equal(t, want, FormatQuota(500000), "rate<=0 => 1")
}

func TestFormatQuota_Tokens(t *testing.T) {
	setDisplay(t, operation_setting.QuotaDisplayTypeTokens, "¤", 1, 7.3)
	assert.Equal(t, "12345", FormatQuota(12345))
}

// ---------------------------------------------------------------------------
// LogQuota — same branch structure, with the " 额度" suffix and symbols.
// ---------------------------------------------------------------------------

func TestLogQuota_AllBranches(t *testing.T) {
	usd := 500000.0 / common.QuotaPerUnit
	cases := []struct {
		name             string
		dtype, symbol    string
		custom, usd      float64
		want             string
	}{
		{"usd", operation_setting.QuotaDisplayTypeUSD, "¤", 1, 7.3, fmt.Sprintf("＄%.6f 额度", usd)},
		{"cny", operation_setting.QuotaDisplayTypeCNY, "¤", 1, 7.3, fmt.Sprintf("¥%.6f 额度", usd*7.3)},
		{"custom", operation_setting.QuotaDisplayTypeCustom, "€", 2, 7.3, fmt.Sprintf("€%.6f 额度", usd*2)},
		{"custom-empty-symbol", operation_setting.QuotaDisplayTypeCustom, "", 2, 7.3, fmt.Sprintf("¤%.6f 额度", usd*2)},
		{"custom-bad-rate", operation_setting.QuotaDisplayTypeCustom, "€", -1, 7.3, fmt.Sprintf("€%.6f 额度", usd*1)},
		{"tokens", operation_setting.QuotaDisplayTypeTokens, "¤", 1, 7.3, "500000 点额度"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setDisplay(t, tc.dtype, tc.symbol, tc.custom, tc.usd)
			assert.Equal(t, tc.want, LogQuota(500000))
		})
	}
}

// ---------------------------------------------------------------------------
// logHelper via LogInfo/LogWarn/LogError — level routing + requestId resolution.
// ---------------------------------------------------------------------------

func TestLogInfo_WritesToDefaultWriterWithLevelAndSystemId(t *testing.T) {
	out, errOut := captureWriters(t)
	LogInfo(context.Background(), "hello")
	assert.Contains(t, out.String(), "[INFO]")
	assert.Contains(t, out.String(), "SYSTEM", "no requestId in ctx => SYSTEM")
	assert.Contains(t, out.String(), "hello")
	assert.Empty(t, errOut.String(), "INFO must not go to the error writer")
}

func TestLogWarn_WritesToErrorWriter(t *testing.T) {
	out, errOut := captureWriters(t)
	LogWarn(context.Background(), "careful")
	assert.Contains(t, errOut.String(), "[WARN]")
	assert.Contains(t, errOut.String(), "careful")
	assert.Empty(t, out.String())
}

func TestLogError_WritesToErrorWriter(t *testing.T) {
	out, errOut := captureWriters(t)
	LogError(context.Background(), "boom")
	assert.Contains(t, errOut.String(), "[ERR]")
	assert.Contains(t, errOut.String(), "boom")
	assert.Empty(t, out.String())
}

func TestLogHelper_UsesRequestIdFromContext(t *testing.T) {
	out, _ := captureWriters(t)
	ctx := context.WithValue(context.Background(), common.RequestIdKey, "req-42")
	LogInfo(ctx, "hi")
	assert.Contains(t, out.String(), "req-42")
	assert.NotContains(t, out.String(), "SYSTEM")
}

func TestLogHelper_NilContextUsesSystem(t *testing.T) {
	out, _ := captureWriters(t)
	LogInfo(nil, "hi") //nolint:staticcheck // deliberately testing nil ctx path
	assert.Contains(t, out.String(), "SYSTEM")
}

// ---------------------------------------------------------------------------
// LogDebug — gated by DebugEnabled; args-formatting branch.
// ---------------------------------------------------------------------------

func TestLogDebug_DisabledProducesNothing(t *testing.T) {
	setDebug(t, false)
	_, errOut := captureWriters(t)
	LogDebug(context.Background(), "should not appear")
	assert.Empty(t, errOut.String())
}

func TestLogDebug_EnabledNoArgs(t *testing.T) {
	setDebug(t, true)
	_, errOut := captureWriters(t)
	LogDebug(context.Background(), "plain message")
	assert.Contains(t, errOut.String(), "[DEBUG]")
	assert.Contains(t, errOut.String(), "plain message")
}

func TestLogDebug_EnabledWithArgsFormats(t *testing.T) {
	setDebug(t, true)
	_, errOut := captureWriters(t)
	LogDebug(context.Background(), "value=%d name=%s", 7, "abc")
	assert.Contains(t, errOut.String(), "value=7 name=abc")
}

// ---------------------------------------------------------------------------
// LogJson — gated + marshal success + marshal error path.
// ---------------------------------------------------------------------------

func TestLogJson_DisabledProducesNothing(t *testing.T) {
	setDebug(t, false)
	_, errOut := captureWriters(t)
	LogJson(context.Background(), "obj", map[string]int{"a": 1})
	assert.Empty(t, errOut.String())
}

func TestLogJson_EnabledMarshalsObject(t *testing.T) {
	setDebug(t, true)
	_, errOut := captureWriters(t)
	LogJson(context.Background(), "obj", map[string]int{"a": 1})
	assert.Contains(t, errOut.String(), "obj")
	assert.Contains(t, errOut.String(), `"a":1`)
}

func TestLogJson_MarshalErrorLogsError(t *testing.T) {
	setDebug(t, true)
	_, errOut := captureWriters(t)
	// A channel cannot be JSON-marshaled => error branch => LogError.
	LogJson(context.Background(), "bad", make(chan int))
	assert.Contains(t, errOut.String(), "json marshal failed")
}

// ---------------------------------------------------------------------------
// SetupLogger + GetCurrentLogPath — file creation and no-op when LogDir empty.
// ---------------------------------------------------------------------------

func withLogDir(t *testing.T, dir string) {
	t.Helper()
	orig := common.LogDir
	d := dir
	common.LogDir = &d
	// SetupLogger rebinds gin writers to a MultiWriter over the file and stores
	// the open handle in currentLogFile/currentLogPath. Snapshot all of it so we
	// can restore the writers AND close the handle SetupLogger opens — otherwise
	// Windows refuses to remove t.TempDir() while the file is still open.
	common.LogWriterMu.Lock()
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	common.LogWriterMu.Unlock()
	currentLogPathMu.Lock()
	origFile, origPath := currentLogFile, currentLogPath
	currentLogPathMu.Unlock()
	t.Cleanup(func() {
		common.LogDir = orig
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr
		common.LogWriterMu.Unlock()
		currentLogPathMu.Lock()
		if currentLogFile != nil && currentLogFile != origFile {
			_ = currentLogFile.Close()
		}
		currentLogFile, currentLogPath = origFile, origPath
		currentLogPathMu.Unlock()
	})
}

func TestSetupLogger_CreatesFileAndExposesPath(t *testing.T) {
	dir := t.TempDir()
	withLogDir(t, dir)
	SetupLogger()

	path := GetCurrentLogPath()
	require.NotEmpty(t, path, "currentLogPath must be set")
	assert.Equal(t, dir, filepath.Dir(path))
	_, err := os.Stat(path)
	require.NoError(t, err, "log file must exist on disk")

	// Writers now fan out to the file too: an INFO line must land in the file.
	LogInfo(context.Background(), "to-file")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "to-file")
}

func TestSetupLogger_EmptyLogDirIsNoOp(t *testing.T) {
	withLogDir(t, "")
	before := GetCurrentLogPath()
	// Point the writer somewhere we can observe it is NOT rebound.
	sentinel := &bytes.Buffer{}
	common.LogWriterMu.Lock()
	gin.DefaultWriter = sentinel
	common.LogWriterMu.Unlock()

	SetupLogger()

	assert.Equal(t, before, GetCurrentLogPath(), "empty LogDir must not change the path")
	common.LogWriterMu.RLock()
	same := gin.DefaultWriter == io.Writer(sentinel)
	common.LogWriterMu.RUnlock()
	assert.True(t, same, "empty LogDir must not rebind the writer")
}
