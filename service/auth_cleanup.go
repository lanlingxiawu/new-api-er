package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const authArtifactCleanupInterval = time.Hour

// StartAuthArtifactCleanup removes expired dashboard Sessions and old
// one-time authentication flows. Only the master instance performs cleanup.
func StartAuthArtifactCleanup() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		cleanupAuthArtifacts()
		ticker := time.NewTicker(authArtifactCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanupAuthArtifacts()
		}
	}()
}

func cleanupAuthArtifacts() {
	now := time.Now()
	sessionSetting := operation_setting.GetUserSessionSnapshot()
	count, err := model.CountUserSessionsCreatedSince(0, now.Add(-time.Hour).Unix())
	if err != nil {
		common.SysError("failed to count hourly user session issuance: " + err.Error())
	} else if count > int64(sessionSetting.HourlyAlertThreshold) {
		common.SysError(fmt.Sprintf(
			"hourly user session issuance exceeded alert threshold: count=%d threshold=%d window_seconds=%d",
			count,
			sessionSetting.HourlyAlertThreshold,
			int64(time.Hour/time.Second),
		))
	}
	if err := model.DeleteExpiredUserSessions(now.Unix()); err != nil {
		common.SysError("failed to delete expired user sessions: " + err.Error())
	}
	if err := model.DeleteOldRevokedUserSessions(now.Unix()); err != nil {
		common.SysError("failed to delete old revoked user sessions: " + err.Error())
	}
	if err := model.DeleteExpiredAuthFlows(now); err != nil {
		common.SysError("failed to delete expired authentication flows: " + err.Error())
	}
}
