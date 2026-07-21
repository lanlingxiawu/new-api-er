package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raiseNotifyLimit sets a generous notification limit for the duration of a test.
func raiseNotifyLimit(t *testing.T) {
	t.Helper()
	origCount := constant.NotifyLimitCount
	origDur := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 1000
	constant.NotificationLimitDurationMinute = 60
	t.Cleanup(func() {
		constant.NotifyLimitCount = origCount
		constant.NotificationLimitDurationMinute = origDur
	})
}

// disableWorkerAndSSRF turns off the worker proxy and SSRF filtering so that
// httptest servers on 127.0.0.1 are reachable.
func disableWorkerAndSSRF(t *testing.T) {
	t.Helper()
	origWorker := system_setting.WorkerUrl
	system_setting.WorkerUrl = ""
	t.Cleanup(func() { system_setting.WorkerUrl = origWorker })
	withFetchSetting(t, func(fs *system_setting.FetchSetting) { fs.EnableSSRFProtection = false })
	InitHttpClient()
}

// ---------------------------------------------------------------------------
// webhook.go
// ---------------------------------------------------------------------------

func TestWebhook_GenerateSignature(t *testing.T) {
	sig := generateSignature("secret", []byte("payload"))
	assert.NotEmpty(t, sig)
	// deterministic
	assert.Equal(t, sig, generateSignature("secret", []byte("payload")))
	// different secret => different signature
	assert.NotEqual(t, sig, generateSignature("other", []byte("payload")))
}

func TestWebhook_SendSuccess(t *testing.T) {
	disableWorkerAndSSRF(t)

	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Webhook-Signature")
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	data := dto.NewNotify("t", "title", "hello %s", []interface{}{"world"})
	err := SendWebhookNotify(srv.URL, "sec", data)
	require.NoError(t, err)
	assert.NotEmpty(t, gotSig)
	assert.Contains(t, string(gotBody), "hello world")
}

func TestWebhook_SendNon2xx(t *testing.T) {
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := SendWebhookNotify(srv.URL, "", dto.NewNotify("t", "ti", "c", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code: 500")
}

func TestWebhook_SendSSRFReject(t *testing.T) {
	origWorker := system_setting.WorkerUrl
	system_setting.WorkerUrl = ""
	t.Cleanup(func() { system_setting.WorkerUrl = origWorker })
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	err := SendWebhookNotify("http://127.0.0.1:9/hook", "", dto.NewNotify("t", "ti", "c", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request reject")
}

// ---------------------------------------------------------------------------
// user_notify.go
// ---------------------------------------------------------------------------

func TestNotifyUser_EmailSkipWhenNoAddress(t *testing.T) {
	raiseNotifyLimit(t)
	// empty NotifyType defaults to email; no email address => skip, nil
	err := NotifyUser(90001, "", dto.UserSetting{}, dto.NewNotify("t", "ti", "c", nil))
	require.NoError(t, err)
}

func TestNotifyUser_WebhookViaHTTPTest(t *testing.T) {
	raiseNotifyLimit(t)
	disableWorkerAndSSRF(t)

	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	us := dto.UserSetting{NotifyType: dto.NotifyTypeWebhook, WebhookUrl: srv.URL, WebhookSecret: "s"}
	err := NotifyUser(90002, "e@x.com", us, dto.NewNotify("t", "ti", "c", nil))
	require.NoError(t, err)
	assert.True(t, hit)
}

func TestNotifyUser_EmptyConfigsSkip(t *testing.T) {
	raiseNotifyLimit(t)
	// webhook type but empty url => nil
	require.NoError(t, NotifyUser(90003, "", dto.UserSetting{NotifyType: dto.NotifyTypeWebhook}, dto.NewNotify("t", "ti", "c", nil)))
	// bark type empty url => nil
	require.NoError(t, NotifyUser(90004, "", dto.UserSetting{NotifyType: dto.NotifyTypeBark}, dto.NewNotify("t", "ti", "c", nil)))
	// gotify missing token => nil
	require.NoError(t, NotifyUser(90005, "", dto.UserSetting{NotifyType: dto.NotifyTypeGotify, GotifyUrl: "http://x"}, dto.NewNotify("t", "ti", "c", nil)))
	// unknown type => nil
	require.NoError(t, NotifyUser(90006, "", dto.UserSetting{NotifyType: "unknown"}, dto.NewNotify("t", "ti", "c", nil)))
}

func TestNotifyUser_LimitExceeded(t *testing.T) {
	// force limit to 0 so any send is rejected
	origCount := constant.NotifyLimitCount
	origDur := constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 0
	constant.NotificationLimitDurationMinute = 60
	t.Cleanup(func() {
		constant.NotifyLimitCount = origCount
		constant.NotificationLimitDurationMinute = origDur
	})
	err := NotifyUser(90007, "e@x.com", dto.UserSetting{NotifyType: dto.NotifyTypeEmail}, dto.NewNotify("t", "ti", "c", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit exceeded")
}

func TestNotify_BarkSuccessAndReject(t *testing.T) {
	raiseNotifyLimit(t)
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	require.NoError(t, sendBarkNotify(srv.URL+"/{{title}}/{{content}}", dto.NewNotify("t", "Ti", "Co", nil)))

	// SSRF reject
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	err := sendBarkNotify("http://127.0.0.1:9/x", dto.NewNotify("t", "Ti", "Co", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request reject")
}

func TestNotify_GotifySuccessAndReject(t *testing.T) {
	raiseNotifyLimit(t)
	disableWorkerAndSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// priority out of range gets clamped to 5
	require.NoError(t, sendGotifyNotify(srv.URL, "token", 99, dto.NewNotify("t", "Ti", "Co "+dto.ContentValueParam, []interface{}{"v"})))

	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	err := sendGotifyNotify("http://127.0.0.1:9", "token", 3, dto.NewNotify("t", "Ti", "Co", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request reject")
}

func TestNotify_EmailContentPlaceholder(t *testing.T) {
	// sendEmailNotify replaces {{value}} placeholders; common.SendEmail will
	// error without SMTP config, but the placeholder substitution path runs.
	err := sendEmailNotify("nobody@example.invalid", dto.NewNotify("t", "Ti", "Hello "+dto.ContentValueParam, []interface{}{"X"}))
	// no SMTP configured => error expected (we only assert it does not panic)
	_ = err
}

func TestNotifyRootUser_NoRoot(t *testing.T) {
	truncate(t)
	// no users seeded => GetRootUser returns empty; NotifyRootUser must not panic
	NotifyRootUser(dto.NotifyTypeChannelUpdate, "subject", "content")
}

func TestNotifyUpstreamModelUpdateWatchers(t *testing.T) {
	truncate(t)
	raiseNotifyLimit(t)
	disableWorkerAndSSRF(t)

	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// enabled admin with webhook
	enabledSetting := dto.UserSetting{
		UpstreamModelUpdateNotifyEnabled: true,
		NotifyType:                       dto.NotifyTypeWebhook,
		WebhookUrl:                       srv.URL,
	}
	settingJSON, err := common.Marshal(enabledSetting)
	require.NoError(t, err)
	adminEnabled := &model.User{
		Id: 91001, Username: "admin_on", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Setting: string(settingJSON), AffCode: "aff91001",
	}
	require.NoError(t, model.DB.Create(adminEnabled).Error)

	// disabled admin (notify flag off) => skipped
	disabledSetting := dto.UserSetting{UpstreamModelUpdateNotifyEnabled: false}
	disabledJSON, _ := common.Marshal(disabledSetting)
	adminDisabled := &model.User{
		Id: 91002, Username: "admin_off", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Setting: string(disabledJSON), AffCode: "aff91002",
	}
	require.NoError(t, model.DB.Create(adminDisabled).Error)

	NotifyUpstreamModelUpdateWatchers("subject", "content")
	assert.True(t, hit)
}

// readAllBody reads the request body fully.
func readAllBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, nil
		}
	}
}
