package controller

// Branch audit of the price monitor: contracts verified as correct. Regression tests for the
// defects the audit found (now fixed) live in price_monitor_branch_audit_regression_test.go.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restorePriceMonitorSettingForAudit puts every price_monitor_setting field back after a test
// that publishes new values through ConfigManager.
func restorePriceMonitorSettingForAudit(t *testing.T) {
	t.Helper()
	original := *price_monitor_setting.GetPriceMonitorSetting()
	t.Cleanup(func() {
		endpoints := original.CustomEndpoints
		if endpoints == nil {
			endpoints = map[string]string{}
		}
		encoded, err := common.Marshal(endpoints)
		require.NoError(t, err)
		require.NoError(t, config.GlobalConfig.UpdateFromMap("price_monitor_setting", map[string]string{
			"enabled":                       strconv.FormatBool(original.Enabled),
			"interval_minutes":              strconv.Itoa(original.IntervalMinutes),
			"timeout_seconds":               strconv.Itoa(original.TimeoutSeconds),
			"include_official":              strconv.FormatBool(original.IncludeOfficial),
			"include_models_dev":            strconv.FormatBool(original.IncludeModelsDev),
			"model_whitelist":               original.ModelWhitelist,
			"custom_endpoints":              string(encoded),
			"upstream_log_queries_per_host": strconv.Itoa(original.UpstreamLogQueriesPerHost),
			"upstream_ratio_refresh_hours":  strconv.Itoa(original.UpstreamRatioRefreshHours),
			"upstream_ratio_max_age_days":   strconv.Itoa(original.UpstreamRatioMaxAgeDays),
		}))
	})
}

// ── loss math ───────────────────────────────────────────────────────────────

// Only dimensions both sides configure, with a positive platform price (the divisor), take part.
func TestPriceMonitorAuditComparableDimensionsSkipZeroPlatformAndOneSidedPrices(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(0),
		Output: floatPointer(10),
		Lanes: []PriceMonitorPriceLane{
			{Key: priceMonitorLaneCacheRead, Price: floatPointer(2)},
			{Key: priceMonitorLaneImageInput},
		},
	}
	source := PriceMonitorPriceCell{
		Mode:  priceMonitorModeToken,
		Input: floatPointer(5),
		Lanes: []PriceMonitorPriceLane{
			{Key: priceMonitorLaneCacheRead, Price: floatPointer(3)},
			{Key: priceMonitorLaneImageInput, Price: floatPointer(4)},
			{Key: priceMonitorLaneAudioInput, Price: floatPointer(1)},
		},
	}
	dimensions, comparable := priceMonitorComparableDimensions(platform, source)
	require.True(t, comparable)
	require.Len(t, dimensions, 1, "free input (divisor 0), missing source output, unset platform image and platform-less audio are skipped")
	assert.Equal(t, priceMonitorLaneCacheRead, dimensions[0].Key)
	factor, ok := priceMonitorMeasuredFactor(platform, source)
	require.True(t, ok)
	assert.InDelta(t, 1.5, factor, 1e-9)

	_, ok = priceMonitorMeasuredFactor(
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0)},
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(9)})
	assert.False(t, ok, "no dimension left: no verdict rather than a division by zero")

	_, comparable = priceMonitorComparableDimensions(platform, PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableSourceFailed})
	assert.False(t, comparable, "a failed source is never compared")
	_, comparable = priceMonitorComparableDimensions(platform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(1)})
	assert.False(t, comparable, "different billing modes are not comparable")
}

// A model given away for free has no measured verdict (documented: the divisor is 0), but the
// repair floor still tells the admin the break-even price.
func TestPriceMonitorAuditFreePlatformPriceStillGetsABreakEvenFloor(t *testing.T) {
	headers := priceMonitorLossHeaders()
	items := []PriceMonitorMatrixItem{{Model: "m", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: {Mode: priceMonitorModeToken, Input: floatPointer(0), optionFields: map[string]float64{"model_ratio": 0}},
		"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(9), Different: true, InputDifferent: true},
	}}}
	contexts := priceMonitorLossContexts(0.9, 0.5)
	applyPriceMonitorLossVerdicts(headers, items, contexts)
	applyPriceMonitorRepairFloors(headers, items, contexts)

	assert.Empty(t, items[0].Prices["channel-a"].LossKinds)
	require.NotNil(t, items[0].RepairFloor)
	assert.InDelta(t, 9/0.9/2, items[0].RepairFloor.Fields["model_ratio"], 1e-9)
	assert.Equal(t, "channel-a", items[0].RepairFloor.Binding["model_ratio"])
}

// An upstream group ratio of 0 (a free upstream group) costs nothing: no measured loss and no
// floor from that channel, while the configured-cost verdict still reports r > g.
func TestPriceMonitorAuditFreeUpstreamGroup(t *testing.T) {
	headers := []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
		{Key: "channel-a", Type: priceSourceChannel},
	}
	items := []PriceMonitorMatrixItem{{Model: "m", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: {Mode: priceMonitorModeToken, Input: floatPointer(10), optionFields: map[string]float64{"model_ratio": 5}},
		"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(20), Different: true, InputDifferent: true},
	}}}
	contexts := map[string]priceMonitorLossContext{"channel-a": {ChannelId: 1, SellFactor: 1, Configured: 1.2, Valid: true, UpstreamRatio: floatPointer(0)}}
	applyPriceMonitorLossVerdicts(headers, items, contexts)
	applyPriceMonitorRepairFloors(headers, items, contexts)

	cell := items[0].Prices["channel-a"]
	assert.Equal(t, []string{priceMonitorLossKindConfigured}, cell.LossKinds)
	require.NotNil(t, cell.MeasuredFactor)
	assert.Zero(t, *cell.MeasuredFactor)
	assert.Empty(t, cell.LossLines, "no line may claim a measured loss for a free upstream group")
	require.NotNil(t, items[0].RepairFloor)
	assert.Empty(t, items[0].RepairFloor.Fields, "a free upstream group sets no break-even floor")
	assert.InDelta(t, 20, items[0].RepairFloor.Highest["model_ratio"], 1e-9, "the raw quote is still shown as a reference")
	assert.InDelta(t, 5, items[0].RepairFloor.Current["model_ratio"], 1e-9)
}

// Per-request pricing: the measured factor and the floor both use the per-request price and the
// channel's own sell factor.
func TestPriceMonitorAuditPerRequestLossAndFloor(t *testing.T) {
	headers := priceMonitorLossHeaders()
	items := []PriceMonitorMatrixItem{{Model: "img", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: {Mode: priceMonitorModeRequest, Price: floatPointer(0.04), optionFields: map[string]float64{"model_price": 0.04}},
		"channel-a":             {Mode: priceMonitorModeRequest, Price: floatPointer(0.03), Different: true, PriceDifferent: true},
		"channel-b":             {Mode: priceMonitorModeRequest, Price: floatPointer(0.05), Different: true, PriceDifferent: true},
	}}}
	contexts := map[string]priceMonitorLossContext{
		"channel-a": {ChannelId: 1, SellFactor: 0.5, Configured: 0.1, Valid: true},
		"channel-b": {ChannelId: 2, SellFactor: 1, Configured: 0.1, Valid: true, UpstreamRatio: floatPointer(0.5)},
	}
	applyPriceMonitorLossVerdicts(headers, items, contexts)
	applyPriceMonitorRepairFloors(headers, items, contexts)

	a := items[0].Prices["channel-a"]
	assert.Equal(t, []string{priceMonitorLossKindMeasured}, a.LossKinds, "0.03 cost vs 0.04 × 0.5 = 0.02 sold")
	require.Len(t, a.LossLines, 1)
	assert.Equal(t, priceMonitorLossLinePrice, a.LossLines[0].Key)
	assert.InDelta(t, 0.03, a.LossLines[0].Cost, 1e-12)
	assert.InDelta(t, 0.02, a.LossLines[0].Sell, 1e-12)
	assert.Empty(t, items[0].Prices["channel-b"].LossKinds, "0.05 × 0.5 = 0.025 cost vs 0.04 sold")

	floor := items[0].RepairFloor
	require.NotNil(t, floor)
	assert.InDelta(t, 0.06, floor.Fields["model_price"], 1e-12, "max(0.03/0.5, 0.05×0.5/1)")
	assert.Equal(t, "channel-a", floor.Binding["model_price"])
	assert.InDelta(t, 0.05, floor.Highest["model_price"], 1e-12)
	assert.InDelta(t, 0.03, floor.Lowest["model_price"], 1e-12)
}

// buildPriceMonitorLossContexts: the sell factor comes from the channel's own groups; a channel
// with no billable group falls back to 1 (code wins over the design doc's "skip"); the cost ratio
// and upstream ratio come from the cost check result.
func TestPriceMonitorAuditBuildLossContexts(t *testing.T) {
	requireDB(t)
	restoreGroupRatio := ratio_setting.GroupRatio2JSONString()
	restoreGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(restoreGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(restoreGroupGroupRatio))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.5,"free":0}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))

	base := 900_000_000 + nextTestID()
	free := &model.Channel{Id: base + 1, Group: "free"}
	ungrouped := &model.Channel{Id: base + 2, Group: ""}
	mixed := &model.Channel{Id: base + 3, Group: "default, vip"}
	unlisted := &model.Channel{Id: base + 4, Group: "default"}
	sourceNames := map[int]string{free.Id: "free", ungrouped.Id: "ungrouped", mixed.Id: "mixed"}
	costs := map[string]PriceMonitorChannelCost{
		strconv.Itoa(free.Id):      {ChannelId: free.Id, CostRatio: 1},
		strconv.Itoa(ungrouped.Id): {ChannelId: ungrouped.Id, CostRatio: 1},
		strconv.Itoa(mixed.Id):     {ChannelId: mixed.Id, CostRatio: 0.7, UpstreamRatio: floatPointer(0.9)},
	}

	contexts := buildPriceMonitorLossContexts([]*model.Channel{nil, free, ungrouped, mixed, unlisted}, sourceNames, costs)
	require.Len(t, contexts, 3, "nil channels and channels without a source column are skipped")
	assert.InDelta(t, 1, contexts["free"].SellFactor, 1e-9)
	assert.InDelta(t, 1, contexts["ungrouped"].SellFactor, 1e-9)
	assert.InDelta(t, 0.5, contexts["mixed"].SellFactor, 1e-9, "the lowest ratio among the channel's groups")
	assert.InDelta(t, 0.7, contexts["mixed"].Configured, 1e-9, "the cost ratio is the one the cost check used")
	require.NotNil(t, contexts["mixed"].UpstreamRatio)
	assert.InDelta(t, 0.9, *contexts["mixed"].UpstreamRatio, 1e-9)
	assert.Nil(t, contexts["free"].UpstreamRatio, "unknown upstream ratio stays nil (list price is used)")
	assert.True(t, contexts["mixed"].Valid)
}

// ── sub2api conversion and billing endpoint ────────────────────────────────

func TestPriceMonitorAuditSub2APIConversionEdges(t *testing.T) {
	plaza := `{"groups":[
	 {"id":1,"rate_multiplier":1,"models":[
	  {"name":"free","pricing":{"billing_mode":"token","input_price":0,"output_price":0}},
	  {"name":"output-only","pricing":{"billing_mode":"token","input_price":0,"output_price":0.00001}},
	  {"name":"negative","pricing":{"billing_mode":"token","input_price":-0.000001,"output_price":0.000002}},
	  {"name":"no-pricing","pricing":null},
	  {"name":"video","pricing":{"billing_mode":"video","per_request_price":0.2}},
	  {"name":"one-dollar","pricing":{"input_price":0.000001}},
	  {"name":"mixed","pricing":{"billing_mode":"token","input_price":0.000002}},
	  {"name":"","pricing":{"billing_mode":"token","input_price":0.000002}}
	 ]},
	 {"id":2,"rate_multiplier":1,"models":[
	  {"name":"mixed","pricing":{"billing_mode":"per_request","per_request_price":0.01}}
	 ]},
	 {"id":3,"rate_multiplier":2,"models":[
	  {"name":"other-group","pricing":{"billing_mode":"token","input_price":0.000001}}
	 ]}
	]}`
	data, err := convertSub2APIPlazaToRatioData([]byte(plaza), plazaBilling(1))
	require.NoError(t, err)
	modelRatio := valueMap(data["model_ratio"])
	assert.InDelta(t, 0, modelRatio["free"], 1e-12, "free in and out is a real price of 0")
	assert.NotContains(t, modelRatio, "output-only", "input 0 with a paid output cannot be expressed as ratios")
	assert.NotContains(t, modelRatio, "negative", "negative prices are ignored")
	assert.NotContains(t, modelRatio, "no-pricing")
	assert.NotContains(t, modelRatio, "video")
	assert.NotContains(t, modelRatio, "other-group", "groups with another multiplier are not the key's group")
	assert.InDelta(t, 0.5, modelRatio["one-dollar"], 1e-12, "$1 per 1M tokens = ratio 0.5; an empty billing mode is per token")
	assert.NotContains(t, valueMap(data["completion_ratio"]), "one-dollar", "no output price, no completion ratio")
	assert.NotContains(t, modelRatio, "mixed", "a per-request price in any matched group wins for the model")
	assert.InDelta(t, 0.01, valueMap(data["model_price"])["mixed"], 1e-12)
}

func TestPriceMonitorAuditFetchSub2APIKeyBilling(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	var mu sync.Mutex
	var current answer
	var seenAuth, seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seenAuth, seenQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		w.WriteHeader(current.status)
		_, _ = w.Write([]byte(current.body))
	}))
	t.Cleanup(server.Close)
	call := func(a answer) (sub2apiKeyBilling, error) {
		mu.Lock()
		current = a
		mu.Unlock()
		return fetchSub2APIKeyBilling(context.Background(), server.Client(), server.URL, "sk-secret")
	}

	billing, err := call(answer{http.StatusOK, sub2apiBilling(0.5, 0.6, "")})
	require.NoError(t, err)
	assert.InDelta(t, 0.6, billing.ResolvedRateMultiplier, 1e-12)
	assert.Equal(t, "Bearer sk-secret", seenAuth, "the key travels in the Authorization header")
	assert.Empty(t, seenQuery, "and never in the URL")

	_, err = call(answer{http.StatusOK, `{"object":"something.else","resolved_rate_multiplier":0.1}`})
	assert.ErrorIs(t, err, errSub2APINotBilling, "gateways that answer 200 to every path are not sub2api")
	_, err = call(answer{http.StatusOK, `<html>ok</html>`})
	assert.ErrorIs(t, err, errSub2APINotBilling)
	_, err = call(answer{http.StatusOK, `{"object":"sub2api.key_billing","padding":"` + strings.Repeat("x", priceMonitorUpstreamBodyLimit) + `"}`})
	assert.ErrorIs(t, err, errSub2APINotBilling, "an oversized body is cut at the limit, not read unbounded")

	_, err = call(answer{http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"no group"}}`})
	assert.True(t, sub2apiBillingRecognised(err))
	assert.Equal(t, priceMonitorRatioReasonSub2APINoGroup, sub2apiBillingErrorReason(err))
	_, err = call(answer{http.StatusForbidden, `forbidden`})
	assert.False(t, sub2apiBillingRecognised(err), "another gateway's 403 does not identify sub2api")
}

func TestPriceMonitorAuditSub2APIBillingErrorReason(t *testing.T) {
	cases := map[int]string{
		http.StatusNotFound:            priceMonitorRatioReasonSub2APIUnsupported,
		http.StatusMethodNotAllowed:    priceMonitorRatioReasonSub2APIUnsupported,
		http.StatusForbidden:           priceMonitorRatioReasonSub2APINoGroup,
		http.StatusUnauthorized:        priceMonitorRatioReasonRejected,
		http.StatusTooManyRequests:     priceMonitorRatioReasonRateLimited,
		http.StatusInternalServerError: priceMonitorRatioReasonUnavailable,
		http.StatusBadGateway:          priceMonitorRatioReasonUnavailable,
	}
	for status, reason := range cases {
		assert.Equal(t, reason, sub2apiBillingErrorReason(&sub2apiBillingStatusError{status: status}), status)
	}
	assert.Equal(t, priceMonitorRatioReasonSub2APIUnsupported, sub2apiBillingErrorReason(errSub2APINotBilling))
	assert.Equal(t, priceMonitorRatioReasonUnavailable, sub2apiBillingErrorReason(context.DeadlineExceeded))
}

// ── official API / log helpers ─────────────────────────────────────────────

func TestPriceMonitorAuditUpstreamErrorReasons(t *testing.T) {
	statuses := map[int]string{
		http.StatusUnauthorized:        priceMonitorRatioReasonRejected,
		http.StatusForbidden:           priceMonitorRatioReasonRejected,
		http.StatusTooManyRequests:     priceMonitorRatioReasonRateLimited,
		http.StatusNotFound:            priceMonitorRatioReasonNotSupported,
		http.StatusMethodNotAllowed:    priceMonitorRatioReasonNotSupported,
		http.StatusInternalServerError: priceMonitorRatioReasonUnavailable,
	}
	for status, reason := range statuses {
		assert.Equal(t, reason, priceMonitorUpstreamErrorReason(&priceMonitorUpstreamStatusError{status: status}), status)
	}
	assert.Equal(t, priceMonitorRatioReasonUnavailable, priceMonitorUpstreamErrorReason(errors.New("dial tcp: refused")))

	logErrors := map[error]string{
		service.ErrUpstreamLogRateLimited:                            priceMonitorRatioReasonRateLimited,
		service.ErrUpstreamLogUnauthorized:                           priceMonitorRatioReasonRejected,
		service.ErrUpstreamLogEndpointMissing:                        priceMonitorRatioReasonNotSupported,
		service.ErrUpstreamLogBaseURLMissing:                         priceMonitorRatioReasonNotSupported,
		service.ErrUpstreamLogBaseURLInvalid:                         priceMonitorRatioReasonNotSupported,
		service.ErrUpstreamLogBusy:                                   priceMonitorRatioReasonWaiting,
		service.ErrUpstreamLogTimeout:                                priceMonitorRatioReasonUnavailable,
		service.ErrUpstreamLogInvalidResp:                            priceMonitorRatioReasonUnavailable,
		fmt.Errorf("wrapped: %w", service.ErrUpstreamLogRateLimited): priceMonitorRatioReasonRateLimited,
	}
	for err, reason := range logErrors {
		assert.Equal(t, reason, priceMonitorLogErrorReason(err), err.Error())
	}
}

func TestPriceMonitorAuditLogItemRatio(t *testing.T) {
	cases := []struct {
		name  string
		other map[string]interface{}
		ratio float64
		ok    bool
	}{
		{"no other", nil, 0, false},
		{"the user's own group ratio wins", map[string]interface{}{"user_group_ratio": 0.3, "group_ratio": 1.0}, 0.3, true},
		{"-1 means no user ratio", map[string]interface{}{"user_group_ratio": -1.0, "group_ratio": 0.8}, 0.8, true},
		{"a free group is a real ratio", map[string]interface{}{"group_ratio": 0.0}, 0, true},
		{"negative ratios are no information", map[string]interface{}{"user_group_ratio": -1.0, "group_ratio": -1.0}, 0, false},
		{"non-numeric ratios are ignored", map[string]interface{}{"group_ratio": "0.8"}, 0, false},
	}
	for _, c := range cases {
		ratio, ok := priceMonitorLogItemRatio(c.other)
		assert.Equal(t, c.ok, ok, c.name)
		assert.InDelta(t, c.ratio, ratio, 1e-12, c.name)
	}
}

func TestPriceMonitorAuditEnabledKeys(t *testing.T) {
	channel := &model.Channel{Key: "k1\n\n k2 \nk3"}
	assert.Equal(t, []string{"k1", "k2", "k3"}, priceMonitorEnabledKeys(channel), "blank lines dropped, keys trimmed")

	channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{
		0: common.ChannelStatusEnabled,
		3: common.ChannelStatusAutoDisabled,
	}}
	assert.Equal(t, []string{"k1", "k2"}, priceMonitorEnabledKeys(channel), "disabled keys are not used; unrecorded keys are enabled")

	assert.Empty(t, priceMonitorEnabledKeys(&model.Channel{}))
}

// priceMonitorUpstreamGet: the access token and user id travel as headers, a non-200 is mapped
// before the body is read, and an oversized body is cut at the limit.
func TestPriceMonitorAuditUpstreamGet(t *testing.T) {
	var mu sync.Mutex
	var auth, user string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth, user = r.Header.Get("Authorization"), r.Header.Get("New-Api-User")
		mu.Unlock()
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/huge":
			_, _ = w.Write([]byte(`{"success":true,"data":"` + strings.Repeat("x", priceMonitorUpstreamBodyLimit) + `"}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":{"group":"vip"}}`))
		}
	}))
	t.Cleanup(server.Close)
	channel := costTestChannel(1, server.URL, true)

	var self priceMonitorUserSelfResponse
	require.NoError(t, priceMonitorUpstreamGet(context.Background(), 2*time.Second, channel, server.URL, "/api/user/self", &self))
	assert.Equal(t, "vip", self.Data.Group)
	assert.Equal(t, "Bearer access-token", auth)
	assert.Equal(t, "42", user)

	err := priceMonitorUpstreamGet(context.Background(), 2*time.Second, channel, server.URL, "/missing", &self)
	assert.Equal(t, priceMonitorRatioReasonNotSupported, priceMonitorUpstreamErrorReason(err))

	err = priceMonitorUpstreamGet(context.Background(), 2*time.Second, channel, server.URL, "/huge", &self)
	require.Error(t, err, "a body over the limit is truncated and fails to parse")
	assert.Equal(t, priceMonitorRatioReasonUnavailable, priceMonitorUpstreamErrorReason(err))
}

// A redirect to another host must not carry the upstream access token, the OpenRouter channel
// key or the sub2api channel key (Go drops Authorization on cross-host redirects; the price
// monitor clients keep the default redirect policy).
func TestPriceMonitorAuditCredentialsNotForwardedOnCrossHostRedirect(t *testing.T) {
	var mu sync.Mutex
	leaked := make([]string, 0)
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		if value := r.Header.Get("Authorization"); value != "" {
			leaked = append(leaked, r.URL.Path+" "+value)
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(target.Close)
	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)
	// Same listener, different host name: the client sees a cross-host redirect.
	otherHost := "http://localhost:" + targetURL.Port()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherHost+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	channel := costTestChannel(1, origin.URL, true)
	var self priceMonitorUserSelfResponse
	_ = priceMonitorUpstreamGet(context.Background(), 2*time.Second, channel, origin.URL, "/api/user/self", &self)

	_, _ = fetchSub2APIKeyBilling(context.Background(), &http.Client{}, origin.URL, "sk-sub2api-secret")

	openRouter := &model.Channel{Id: 9, Key: "sk-or-secret", BaseURL: &origin.URL, Type: constant.ChannelTypeOpenRouter}
	previous := pricingSourceChannel
	pricingSourceChannel = func(int) (*model.Channel, error) { return openRouter, nil }
	t.Cleanup(func() { pricingSourceChannel = previous })
	_, _ = fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{ID: 9, Name: "or", BaseURL: origin.URL, Endpoint: openRouterPricingEndpoint}}, 2)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, hits, "all three requests followed the redirect")
	assert.Empty(t, leaked, "no credential may reach the redirect target")
}

func TestPriceMonitorAuditApplyRatioObservationStates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	known := PriceMonitorChannelCost{ChannelId: 1, UpstreamRatio: floatPointer(0.8), ObservedAt: 100, AttemptedAt: 100}

	waiting := known
	applyPriceMonitorRatioObservation(&waiting, priceMonitorRatioObservation{reason: priceMonitorRatioReasonWaiting}, now)
	assert.Equal(t, int64(100), waiting.AttemptedAt, "queued for the log budget: not counted as an attempt")
	assert.Empty(t, waiting.Reason, "a known ratio is still valid; waiting is not shown over it")

	unknown := PriceMonitorChannelCost{ChannelId: 2}
	applyPriceMonitorRatioObservation(&unknown, priceMonitorRatioObservation{reason: priceMonitorRatioReasonWaiting}, now)
	assert.Equal(t, priceMonitorRatioReasonWaiting, unknown.Reason)
	assert.Zero(t, unknown.AttemptedAt)

	limited := known
	applyPriceMonitorRatioObservation(&limited, priceMonitorRatioObservation{reason: priceMonitorRatioReasonRateLimited}, now)
	assert.Equal(t, priceMonitorRatioReasonRateLimited, limited.Reason, "rate limiting is always reported")
	assert.Equal(t, int64(100), limited.AttemptedAt, "and retried first next round")
	require.NotNil(t, limited.UpstreamRatio)

	failed := known
	applyPriceMonitorRatioObservation(&failed, priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}, now)
	assert.Equal(t, now.Unix(), failed.AttemptedAt)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, failed.Reason)
	require.NotNil(t, failed.UpstreamRatio, "an unexpired ratio survives a failed refresh")
	assert.Equal(t, int64(100), failed.ObservedAt)

	refreshed := failed
	applyPriceMonitorRatioObservation(&refreshed, priceMonitorRatioObservation{ok: true, ratio: 0, source: priceMonitorRatioSourceLog, group: "free"}, now)
	require.NotNil(t, refreshed.UpstreamRatio)
	assert.Zero(t, *refreshed.UpstreamRatio, "a free group (ratio 0) is a valid observation")
	assert.Empty(t, refreshed.Reason)
	assert.Equal(t, now.Unix(), refreshed.ObservedAt)
}

// ── HTTP handlers ───────────────────────────────────────────────────────────

func TestPriceMonitorAuditRunPriceMonitorGuards(t *testing.T) {
	t.Run("only the master node runs checks", func(t *testing.T) {
		previous := common.IsMasterNode
		common.IsMasterNode = false
		t.Cleanup(func() { common.IsMasterNode = previous })
		ctx, rec := newCtx(t, http.MethodPost, "/api/price_monitor/run", nil)
		RunPriceMonitor(ctx)
		out := decodeResp(t, rec)
		assert.False(t, out.Success)
		assert.Equal(t, i18n.T(ctx, i18n.MsgPriceMonitorMasterRequired), out.Message)
		assert.NotEqual(t, i18n.MsgPriceMonitorMasterRequired, out.Message, "a translated message, not the key")
	})

	t.Run("a second run is refused while one is in progress", func(t *testing.T) {
		require.True(t, priceMonitorRunning.CompareAndSwap(false, true), "precondition: no check running")
		t.Cleanup(func() { priceMonitorRunning.Store(false) })
		assert.False(t, triggerPriceMonitorCheck(), "single flight")
		ctx, rec := newCtx(t, http.MethodPost, "/api/price_monitor/run", nil)
		RunPriceMonitor(ctx)
		out := decodeResp(t, rec)
		assert.False(t, out.Success)
		assert.Equal(t, i18n.T(ctx, i18n.MsgPriceMonitorAlreadyRunning), out.Message)
	})
}

func TestPriceMonitorAuditPublicQueryPassword(t *testing.T) {
	snapshot := applyPriceSnapshotWithFloor("gpt-4o", completionFloor(3, 6))
	snapshot.SourceHeaders[1].ChannelId = 11
	snapshot.AccessPassword = "share123"
	snapshot.PasswordExpireAt = time.Now().Add(time.Hour).Unix()
	useApplyPriceEnv(t, snapshot)

	query := func(body string) apiResp {
		ctx, rec := newRawCtx(t, http.MethodPost, "/api/price_monitor/public_query", body)
		PublicPriceMonitorQuery(ctx)
		return decodeResp(t, rec)
	}
	for name, body := range map[string]string{
		"wrong password":         `{"password":"share124"}`,
		"prefix of the password": `{"password":"share12"}`,
		"empty password":         `{"password":""}`,
	} {
		out := query(body)
		assert.False(t, out.Success, name)
		assert.NotContains(t, string(out.Data), "gpt-4o", name)
	}
	assert.False(t, query(`not json`).Success)

	out := query(`{"password":"share123"}`)
	require.True(t, out.Success, out.Message)
	assert.NotContains(t, string(out.Data), "channel_id")
	stored := getPriceMonitorStore().Get()
	assert.Equal(t, 11, stored.SourceHeaders[1].ChannelId, "stripping the public copy must not touch the stored snapshot")
	assert.NotNil(t, stored.MatrixItems[0].RepairFloor)

	expired := snapshot
	expired.PasswordExpireAt = time.Now().Add(-time.Second).Unix()
	require.NoError(t, getPriceMonitorStore().Save(expired))
	assert.False(t, query(`{"password":"share123"}`).Success, "an expired password is refused")
}

// Below the break-even floor the whole request is refused with the violations; force applies it.
func TestPriceMonitorAuditApplyPriceBelowFloorAndForce(t *testing.T) {
	keepPricingRatios(t)
	floor := &PriceMonitorRepairFloor{
		Mode:    priceMonitorModeToken,
		Fields:  map[string]float64{"model_ratio": 2},
		Display: map[string]float64{"model_ratio": 4},
		Binding: map[string]string{"model_ratio": "channel-a"},
	}
	useApplyPriceEnv(t, applyPriceSnapshotWithFloor("gpt-4o", floor))
	seedPricingRatios(t, "gpt-4o", "3", "1")

	body := func(force bool) string {
		return `{"checked_at":100,"pricing_version":0,"force":` + strconv.FormatBool(force) +
			`,"items":[{"model":"gpt-4o","fields":{"model_ratio":1},"expected":{"model_ratio":3}}]}`
	}
	_, response := performApplyPrice(t, body(false))
	require.Equal(t, false, response["success"])
	assert.Equal(t, "PRICE_BELOW_FLOOR", response["error_code"])
	violations := response["data"].(map[string]any)["violations"].([]any)
	require.Len(t, violations, 1)
	violation := violations[0].(map[string]any)
	assert.Equal(t, "model_ratio", violation["field"])
	assert.InDelta(t, 1, violation["value"], 1e-9)
	assert.InDelta(t, 2, violation["floor"], 1e-9)
	assert.Equal(t, "channel-a", violation["binding"])
	assert.Equal(t, true, violation["submitted"])
	stored, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	assert.InDelta(t, 3, stored, 1e-9, "nothing is written")

	_, response = performApplyPrice(t, body(true))
	require.Equal(t, true, response["success"], response["message"])
	stored, _ = applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	assert.InDelta(t, 1, stored, 1e-9, "an explicit override is applied")
}

func TestPriceMonitorAuditApplyPerRequestPriceBelowFloor(t *testing.T) {
	snapshot := applyPriceSnapshot(100, priceMonitorModeRequest)
	snapshot.MatrixItems[0].RepairFloor = &PriceMonitorRepairFloor{
		Mode:    priceMonitorModeRequest,
		Fields:  map[string]float64{"model_price": 0.05},
		Display: map[string]float64{"model_price": 0.05},
	}
	useApplyPriceEnv(t, snapshot)
	seedApplyPriceOption(t, "ModelPrice", `{"gpt-4o":0.08}`)

	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"model_price":0.01},"expected":{"model_price":0.08}}]}`)
	require.Equal(t, false, response["success"])
	assert.Equal(t, "PRICE_BELOW_FLOOR", response["error_code"])
	stored, _ := applyPriceOptionValue(t, "ModelPrice", "gpt-4o")
	assert.InDelta(t, 0.08, stored, 1e-12)

	_, response = performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o","fields":{"model_price":-1},"expected":{"model_price":0.08}}]}`)
	require.Equal(t, false, response["success"], "negative prices are refused")
	assert.Nil(t, response["error_code"])
}

// ── settings ────────────────────────────────────────────────────────────────

func TestPriceMonitorAuditSettingsRequestCostCheckBoundaries(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	valid := []priceMonitorSettingsRequest{
		{IntervalMinutes: 5, TimeoutSeconds: 1, UpstreamLogQueriesPerHost: intPtr(0)},
		{IntervalMinutes: 5, TimeoutSeconds: 120, UpstreamRatioRefreshHours: intPtr(price_monitor_setting.MaxUpstreamRatioRefreshHours)},
		{IntervalMinutes: 5, TimeoutSeconds: 10, UpstreamRatioMaxAgeDays: intPtr(1)},
	}
	for _, request := range valid {
		_, ok := validatePriceMonitorSettingsRequest(request)
		assert.True(t, ok, "%+v", request)
	}
	values, _ := validatePriceMonitorSettingsRequest(valid[0])
	assert.Equal(t, "0", values["upstream_log_queries_per_host"], "0 is meaningful: logs off")

	invalid := []priceMonitorSettingsRequest{
		{IntervalMinutes: 5, TimeoutSeconds: 10, UpstreamLogQueriesPerHost: intPtr(-1)},
		{IntervalMinutes: 5, TimeoutSeconds: 10, UpstreamRatioRefreshHours: intPtr(-6)},
		{IntervalMinutes: 5, TimeoutSeconds: 10, CustomEndpoints: map[string]string{"0": "/api/pricing"}},
		{IntervalMinutes: 5, TimeoutSeconds: 10, CustomEndpoints: map[string]string{"abc": "/api/pricing"}},
		{IntervalMinutes: 5, TimeoutSeconds: 10, CustomEndpoints: map[string]string{"7": "javascript://x"}},
	}
	for _, request := range invalid {
		values, ok := validatePriceMonitorSettingsRequest(request)
		assert.False(t, ok, "%+v", request)
		assert.Nil(t, values)
	}
}

// A client that does not send the cost-check fields (the pre-change request shape) saves fine and
// does not overwrite the stored cost-check values.
func TestPriceMonitorAuditSettingsSaveWithoutCostCheckFields(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	restorePriceMonitorSettingForAudit(t)
	before := *price_monitor_setting.GetPriceMonitorSetting()

	values, ok := validatePriceMonitorSettingsRequest(priceMonitorSettingsRequest{IntervalMinutes: 90, TimeoutSeconds: 15, CustomEndpoints: map[string]string{"7": "/api/ratio_config"}})
	require.True(t, ok)
	_, err := model.SaveConfigGroup("price_monitor_setting", values)
	require.NoError(t, err)

	after := price_monitor_setting.GetPriceMonitorSetting()
	assert.Equal(t, 90, after.IntervalMinutes)
	assert.Equal(t, "/api/ratio_config", after.CustomEndpointFor(7))
	assert.Equal(t, before.UpstreamLogQueriesPerHost, after.UpstreamLogQueriesPerHost)
	var option model.Option
	require.NoError(t, model.DB.Where(model.Option{Key: "price_monitor_setting.interval_minutes"}).Take(&option).Error)
	assert.Equal(t, "90", option.Value)
}

// ── scheduling ──────────────────────────────────────────────────────────────

func TestPriceMonitorAuditCheckDueEdges(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	assert.False(t, priceMonitorCheckDue(false, 5, 0, 0, now), "disabled never runs")
	assert.True(t, priceMonitorCheckDue(true, 5, 0, 0, now), "never ran: due")
	assert.False(t, priceMonitorCheckDue(true, 1, now.Unix()-60, 0, now), "an interval below 5 minutes is raised to 5")
	assert.True(t, priceMonitorCheckDue(true, 1, now.Unix()-300, 0, now))
	assert.False(t, priceMonitorCheckDue(true, 360, now.Unix()-7*3600, now.Unix()-60, now), "a recent failed attempt postpones the next run")
}

func TestPriceMonitorAuditPasswordAlphabet(t *testing.T) {
	for _, ambiguous := range "0O1lI" {
		assert.NotContains(t, priceMonitorPasswordAlphabet, string(ambiguous))
	}
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		password, err := generatePriceMonitorPassword(8)
		require.NoError(t, err)
		require.Len(t, password, 8)
		for _, char := range password {
			require.Contains(t, priceMonitorPasswordAlphabet, string(char))
		}
		seen[password] = struct{}{}
	}
	assert.Greater(t, len(seen), 45, "passwords are random")
}

// The snapshot file never stores channel keys or upstream access tokens: channel costs keep only
// a truncated digest, and the channel cost list drops even that.
func TestPriceMonitorAuditSnapshotHoldsNoCredentials(t *testing.T) {
	channel := costTestChannel(3, "https://up.example", true)
	channel.Key = "sk-live-channel-secret"
	fingerprint := priceMonitorRatioSourceFingerprint(channel)
	assert.Len(t, fingerprint, 16)

	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	require.NoError(t, store.Save(PriceMonitorSnapshot{CheckedAt: 1, ChannelCosts: map[string]PriceMonitorChannelCost{
		"3": {ChannelId: 3, SourceFingerprint: fingerprint, UpstreamRatio: floatPointer(1)},
	}}))
	file, err := os.ReadFile(store.path)
	require.NoError(t, err)
	for _, secret := range []string{"sk-live-channel-secret", "live-channel-secret", "access-token"} {
		assert.NotContains(t, string(file), secret)
	}
}
