package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestAIAuditSQLiteFixtureRestoresTokenLookup(t *testing.T) {
	requireDB(t)
	user := mkUser(t, nil)
	token := mkToken(t, user.Id, nil)
	for _, fixture := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"model_list", func(t *testing.T) { setupModelListControllerTestDB(t) }},
		{"token", func(t *testing.T) { setupTokenControllerTestDB(t) }},
		{"responses_websocket", func(t *testing.T) { setupResponsesWSRequestTest(t) }},
		{"channel_multi_key", TestMultiKeyEnableRestoresOnlyExhaustedChannels},
		{"api_token_audit", TestAPITokenAuditDatabaseMatrix},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Keep the regression from poisoning unrelated tests when it fails.
			t.Cleanup(model.InitColumnNames)
			t.Run("isolated_sqlite", fixture.run)
			got, err := model.GetTokenByKey(token.Key, true)
			require.NoError(t, err, "restored database must also restore dialect-specific column names")
			require.Equal(t, token.Id, got.Id)
		})
	}
}
