package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/infini_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func infiniV2TestCredentials(t *testing.T) ed25519.PublicKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err)
	previous := *infini_setting.GetInfiniSetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"key_id": previous.KeyID, "private_key": previous.PrivateKey}))
	})
	require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"key_id": strings.Repeat("A", 32), "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}))
	return public
}

func TestInfiniV2PostSignsExactBody(t *testing.T) {
	public := infiniV2TestCredentials(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "/v2/acquiring/order", r.URL.Path)
		require.JSONEq(t, `{"amount":"1.00","currency":"USD"}`, string(body))
		payload := strings.Join([]string{"INFINI-OPENAPI-V2", r.Header.Get("X-API-Key-ID"), r.Method, r.URL.RequestURI(), r.Header.Get("X-API-Timestamp"), r.Header.Get("X-API-Nonce"), r.Header.Get("Digest")}, "\n") + "\n"
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Authorization"), `Ed25519 signature="`), `"`))
		require.NoError(t, err)
		require.True(t, ed25519.Verify(public, []byte(payload), sig))
		_, _ = io.WriteString(w, `{"code":0,"data":{"order_id":"ord-1","checkout_url":"https://example.com/pay"}}`)
	}))
	defer server.Close()
	previous := infiniAPIBaseURL
	t.Cleanup(func() { infiniAPIBaseURL = previous })
	infiniAPIBaseURL = func() string { return server.URL }
	_, status, err := infiniPost(context.Background(), "/v2/acquiring/order", map[string]string{"amount": "1.00", "currency": "USD"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = infiniPost(canceled, "/v2/acquiring/order", map[string]string{"amount": "1.00"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestInfiniV2ConfigurationPrivacyAndValidation(t *testing.T) {
	infiniV2TestCredentials(t)
	setOptionMapForTest(t)
	common.OptionMapRWMutex.Lock()
	oldPrivate, hadPrivate := common.OptionMap["infini_setting.private_key"]
	oldID, hadID := common.OptionMap["infini_setting.key_id"]
	common.OptionMap["infini_setting.private_key"] = "must-not-be-returned"
	common.OptionMap["infini_setting.key_id"] = strings.Repeat("A", 32)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadPrivate {
			common.OptionMap["infini_setting.private_key"] = oldPrivate
		} else {
			delete(common.OptionMap, "infini_setting.private_key")
		}
		if hadID {
			common.OptionMap["infini_setting.key_id"] = oldID
		} else {
			delete(common.OptionMap, "infini_setting.key_id")
		}
	})
	c, rec := newCtx(t, "GET", "/api/option", nil)
	GetOptions(c)
	require.NotContains(t, rec.Body.String(), "must-not-be-returned")
	require.NotContains(t, rec.Body.String(), `"key":"infini_setting.private_key"`)
	require.Contains(t, rec.Body.String(), "infini_setting.key_id")
	for _, key := range []string{"infini_setting.key_id", "infini_setting.private_key"} {
		body := map[string]string{"key": key, "value": "invalid"}
		c, rec := newCtx(t, "PUT", "/api/option", body)
		c.Set("role", common.RoleRootUser)
		UpdateOption(c)
		require.False(t, decodeResp(t, rec).Success)
	}
	previous := infini_setting.GetInfiniSetting().PrivateKey
	c, rec = newCtx(t, "PUT", "/api/option", map[string]string{"key": "infini_setting.private_key", "value": "  "})
	c.Set("role", common.RoleRootUser)
	UpdateOption(c)
	require.True(t, decodeResp(t, rec).Success)
	require.Equal(t, previous, infini_setting.GetInfiniSetting().PrivateKey)
}

func TestInfiniV2MissingCredentialsPreservesWebhook(t *testing.T) {
	infiniV2TestCredentials(t)
	require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"private_key": ""}))
	previousEnabled, previousHook := setting.InfiniEnabled, setting.InfiniWebhookSecret
	previousCompliance := operation_setting.GetPaymentSetting().ComplianceConfirmed
	previousVersion := operation_setting.GetPaymentSetting().ComplianceTermsVersion
	t.Cleanup(func() {
		setting.InfiniEnabled = previousEnabled
		setting.InfiniWebhookSecret = previousHook
		operation_setting.GetPaymentSetting().ComplianceConfirmed = previousCompliance
		operation_setting.GetPaymentSetting().ComplianceTermsVersion = previousVersion
	})
	setting.InfiniEnabled = true
	setting.InfiniWebhookSecret = "historical-webhook-secret"
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	require.False(t, isInfiniTopUpEnabled())
	require.True(t, isInfiniWebhookEnabled())
	for _, tc := range []struct {
		name             string
		expired, invalid bool
		status           int
	}{
		{"valid historical signature", false, false, http.StatusOK},
		{"bad signature", false, true, http.StatusUnauthorized},
		{"expired signature", true, false, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"event":"order.create"}`
			ts := time.Now().Unix()
			if tc.expired {
				ts -= 301
			}
			timestamp := strconv.FormatInt(ts, 10)
			mac := hmac.New(sha256.New, []byte(setting.InfiniWebhookSecret))
			_, err := mac.Write([]byte(timestamp + ".historical-event." + body))
			require.NoError(t, err)
			signature := hex.EncodeToString(mac.Sum(nil))
			if tc.invalid {
				signature = "invalid"
			}
			c, rec := newRawCtx(t, "POST", "/api/infini/webhook", body)
			c.Request.Header.Set("X-Webhook-Timestamp", timestamp)
			c.Request.Header.Set("X-Webhook-Event-Id", "historical-event")
			c.Request.Header.Set("X-Webhook-Signature", signature)
			InfiniWebhook(c)
			require.Equal(t, tc.status, rec.Code)
		})
	}
}

func infiniV2TestDatabase(t *testing.T) {
	t.Helper()
	if model.DB == nil {
		// No configured SQL_DSN: isolated SQLite fallback, never a shared DB.
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Option{}))
		model.DB = db
		model.InitColumnNames()
		t.Cleanup(func() { model.DB = nil; require.NoError(t, sqlDB.Close()) })
	}
	requireDB(t)
}

func TestInfiniV2SettingsPersistence(t *testing.T) {
	infiniV2TestDatabase(t)
	infiniV2TestCredentials(t)
	credentials := *infini_setting.GetInfiniSetting()
	previousDB := model.DB
	tx := model.DB.Begin()
	require.NoError(t, tx.Error)
	model.DB = tx
	t.Cleanup(func() { model.DB = previousDB; require.NoError(t, tx.Rollback().Error) })
	for key, value := range map[string]string{"infini_setting.key_id": credentials.KeyID, "infini_setting.private_key": credentials.PrivateKey} {
		common.OptionMapRWMutex.RLock()
		previous, exists := common.OptionMap[key]
		common.OptionMapRWMutex.RUnlock()
		t.Cleanup(func() {
			common.OptionMapRWMutex.Lock()
			defer common.OptionMapRWMutex.Unlock()
			if exists {
				common.OptionMap[key] = previous
			} else {
				delete(common.OptionMap, key)
			}
		})
		c, rec := newCtx(t, "PUT", "/api/option", map[string]string{"key": key, "value": value})
		c.Set("role", common.RoleRootUser)
		UpdateOption(c)
		require.True(t, decodeResp(t, rec).Success)
		var saved model.Option
		require.NoError(t, tx.Where(map[string]any{"key": key}).First(&saved).Error)
		require.Equal(t, strings.TrimSpace(value), saved.Value)
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"key_id": "", "private_key": ""}))
	require.False(t, infini_setting.CredentialsConfigured())
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"infini_setting.key_id": credentials.KeyID, "infini_setting.private_key": credentials.PrivateKey}))
	require.True(t, infini_setting.CredentialsConfigured())
}

func TestInfiniV2CredentialSaveFailures(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("INFINI_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("dedicated MySQL fixture not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("INFINI_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("dedicated PostgreSQL fixture not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			if dialect != "sqlite" {
				// Fault triggers must never be installed on a shared project database.
				var databaseName string
				nameSQL := "SELECT DATABASE()"
				if dialect == "postgres" {
					nameSQL = "SELECT current_database()"
				}
				require.NoError(t, db.Raw(nameSQL).Scan(&databaseName).Error)
				require.True(t, strings.HasPrefix(databaseName, "infini_save_test"), "use a dedicated infini_save_test database for fault injection")
			}
			require.NoError(t, db.AutoMigrate(&model.Option{}))
			var version string
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database: %s %s", dialect, version)
			previousDB := model.DB
			model.DB = db
			t.Cleanup(func() { model.DB = previousDB })
			for _, field := range []string{"key_id", "private_key"} {
				for _, failure := range []string{"lookup", "insert", "update_new", "update_existing", "success"} {
					t.Run(field+"/"+failure, func(t *testing.T) {
						infiniV2TestCredentials(t)
						old := *infini_setting.GetInfiniSetting()
						infiniV2TestCredentials(t)
						candidate := *infini_setting.GetInfiniSetting()
						candidate.KeyID = strings.Repeat("B", 32)
						require.NoError(t, config.GlobalConfig.UpdateFromMap("infini_setting", map[string]string{"key_id": old.KeyID, "private_key": old.PrivateKey}))
						key := "infini_setting." + field
						oldValue, newValue := old.PrivateKey, candidate.PrivateKey
						if field == "key_id" {
							oldValue, newValue = old.KeyID, candidate.KeyID
						}
						common.OptionMapRWMutex.Lock()
						previous, existed := common.OptionMap[key]
						common.OptionMap[key] = oldValue
						common.OptionMapRWMutex.Unlock()
						t.Cleanup(func() {
							common.OptionMapRWMutex.Lock()
							defer common.OptionMapRWMutex.Unlock()
							if existed {
								common.OptionMap[key] = previous
							} else {
								delete(common.OptionMap, key)
							}
						})
						require.NoError(t, db.Where(map[string]any{"key": key}).Delete(&model.Option{}).Error)
						t.Cleanup(func() { require.NoError(t, db.Where(map[string]any{"key": key}).Delete(&model.Option{}).Error) })
						if failure == "update_existing" {
							require.NoError(t, db.Create(&model.Option{Key: key, Value: oldValue}).Error)
						}
						if failure == "lookup" {
							model.DB = db.Table("infini_missing_options")
							t.Cleanup(func() { model.DB = db })
						} else if failure != "success" {
							event := "UPDATE"
							if failure == "insert" {
								event = "INSERT"
							}
							switch dialect {
							case "sqlite":
								require.NoError(t, db.Exec("CREATE TRIGGER infini_reject_write BEFORE "+event+" ON options BEGIN SELECT RAISE(ABORT, 'sensitive_db_marker'); END").Error)
								t.Cleanup(func() { require.NoError(t, db.Exec("DROP TRIGGER infini_reject_write").Error) })
							case "mysql":
								require.NoError(t, db.Exec("CREATE TRIGGER infini_reject_write BEFORE "+event+" ON options FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'sensitive_db_marker'").Error)
								t.Cleanup(func() { require.NoError(t, db.Exec("DROP TRIGGER infini_reject_write").Error) })
							case "postgres":
								require.NoError(t, db.Exec("CREATE FUNCTION infini_reject_write_fn() RETURNS trigger AS 'BEGIN RAISE EXCEPTION ''sensitive_db_marker''; END' LANGUAGE plpgsql").Error)
								require.NoError(t, db.Exec("CREATE TRIGGER infini_reject_write BEFORE "+event+" ON options FOR EACH ROW EXECUTE PROCEDURE infini_reject_write_fn()").Error)
								t.Cleanup(func() {
									require.NoError(t, db.Exec("DROP TRIGGER infini_reject_write ON options").Error)
									require.NoError(t, db.Exec("DROP FUNCTION infini_reject_write_fn()").Error)
								})
							}
						}
						c, rec := newCtx(t, "PUT", "/api/option", map[string]string{"key": key, "value": newValue})
						c.Set("role", common.RoleRootUser)
						UpdateOption(c)
						success := failure == "success"
						require.Equal(t, success, decodeResp(t, rec).Success, "must report the actual persistence result")
						require.NotContains(t, rec.Body.String(), "sensitive_db_marker")
						require.NotContains(t, rec.Body.String(), "PRIVATE KEY")
						expected := old
						if success {
							if field == "key_id" {
								expected.KeyID = candidate.KeyID
							} else {
								expected.PrivateKey = strings.TrimSpace(candidate.PrivateKey)
							}
							oldValue = strings.TrimSpace(newValue)
						}
						require.Equal(t, expected, *infini_setting.GetInfiniSetting(), "publish credentials only after persistence succeeds")
						common.OptionMapRWMutex.RLock()
						cached := common.OptionMap[key]
						common.OptionMapRWMutex.RUnlock()
						require.Equal(t, oldValue, cached)
						var stored []model.Option
						require.NoError(t, db.Where(map[string]any{"key": key}).Find(&stored).Error)
						if failure == "update_existing" || success {
							require.Len(t, stored, 1)
							require.Equal(t, oldValue, stored[0].Value)
						} else {
							require.Empty(t, stored, "failed transaction must not leave an empty option row")
						}
					})
				}
			}
		})
	}
}

func TestInfiniV2CheckoutResponses(t *testing.T) {
	infiniV2TestDatabase(t)
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		success bool
	}{
		{"envelope", 200, `{"code":0,"data":{"order_id":"ord-1","checkout_url":"https://example.com/pay"}}`, true},
		{"direct", 200, `{"order_id":"ord-1","checkout_url":"https://example.com/pay"}`, true},
		{"unauthorized", 401, `{"code":401}`, false},
		{"forbidden", 403, `{"code":403}`, false},
		{"unavailable", 500, `{}`, false},
		{"business failure", 200, `{"code":42,"data":{"order_id":"ord-1","checkout_url":"https://example.com/pay"}}`, false},
		{"malformed", 200, `{`, false},
		{"missing id", 200, `{"checkout_url":"https://example.com/pay"}`, false},
		{"missing url", 200, `{"order_id":"ord-1"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`)
			infiniV2TestCredentials(t)
			u := mkUser(t, nil)
			t.Cleanup(func() { require.NoError(t, model.DB.Where("user_id = ?", u.Id).Delete(&model.TopUp{}).Error) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v2/acquiring/order", r.URL.Path)
				var payload map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				require.NotContains(t, payload, "notify_url")
				require.Equal(t, "USD", payload["currency"])
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			previous := infiniAPIBaseURL
			t.Cleanup(func() { infiniAPIBaseURL = previous })
			infiniAPIBaseURL = func() string { return server.URL }
			c, rec := newCtx(t, http.MethodPost, "/api/user/self/infini/pay", map[string]any{"amount": 1})
			asUser(c, u.Id)
			RequestInfiniPay(c)
			var result struct {
				Message string `json:"message"`
				Data    struct {
					URL string `json:"payment_url"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &result))
			require.Equal(t, tc.success, result.Message == "success", rec.Body.String())
			if tc.success {
				require.Equal(t, "https://example.com/pay", result.Data.URL)
			} else {
				require.Empty(t, result.Data.URL)
			}
			var order model.TopUp
			require.NoError(t, model.DB.Where("user_id = ?", u.Id).First(&order).Error)
			if tc.success {
				require.Equal(t, "ord-1", order.ProviderOrderId)
				require.Equal(t, common.TopUpStatusPending, order.Status)
			} else {
				require.Equal(t, common.TopUpStatusFailed, order.Status)
			}
		})
	}
}
