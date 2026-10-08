package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression (review M4): a charged stream that failed keeps the upstream's
// text only in admin_info.stream_error; the admin export lost it once content
// held only our note. The admin export's 详情 carries it again, and a user's
// own export never does.
func TestExportLogs_StreamErrorOnlyInAdminExport(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	now := time.Now().Unix()
	const upstreamText = "overloaded for group vip"
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: u.Id, Username: u.Username, CreatedAt: now, Type: model.LogTypeConsume,
		Content: "note; Upstream stream failed. Please retry. (request id: rid-se-1)", RequestId: "rid-se-1",
		Other: `{"admin_info":{"use_channel":["7"],"stream_error":"` + upstreamText + `"}}`,
	}).Error)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", u.Id).Delete(&model.Log{}) })
	span := fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-60, now+60)

	userCtx, userRec := newCtx(t, "GET", "/api/log/self/export?"+span, nil)
	asUser(userCtx, u.Id)
	ExportUserLogs(userCtx)
	userDetails := exportedDetails(t, userRec.Body.Bytes())
	require.Len(t, userDetails, 1)
	assert.Equal(t, "note; Upstream stream failed. Please retry. (request id: rid-se-1)", userDetails[0])
	assert.False(t, strings.Contains(userDetails[0], upstreamText), "admin_info never reaches the user's file")

	adminCtx, adminRec := newCtx(t, "GET", "/api/log/export?"+span+"&username="+u.Username, nil)
	asAdmin(adminCtx, nextTestID())
	ExportAllLogs(adminCtx)
	adminDetails := exportedDetails(t, adminRec.Body.Bytes())
	require.Len(t, adminDetails, 1)
	assert.Equal(t, "note; Upstream stream failed. Please retry. (request id: rid-se-1); stream_error: "+upstreamText, adminDetails[0])
}
