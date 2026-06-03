package service

// monitor_service.go — Simplified monitoring for group model availability.
//
// This service has been simplified from complex health metrics to simple
// availability tracking. It checks whether models in each group have had
// successful requests in the last 30 minutes.

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// UpdateGroupModelStatusFromChannelTest updates group/model statuses after channel testing.
// Called after AutomaticallyTestChannels() to refresh availability data.
func UpdateGroupModelStatusFromChannelTest(ctx context.Context) error {
	// Get all groups from the channels table
	channels, err := model.GetAllChannels(0, 10000, false, false)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] failed to get channels: %v", err))
		return err
	}

	// Collect all groups
	groupMap := make(map[string]bool)
	for _, ch := range channels {
		for _, g := range ch.GetGroups() {
			if g != "" {
				groupMap[g] = true
			}
		}
	}

	// Test availability for each group
	var errors []error
	for groupName := range groupMap {
		if err := TestGroupModelAvailability(ctx, groupName); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[monitor] failed to test group %s: %v", groupName, err))
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] %d groups failed availability test", len(errors)))
	}

	return nil
}
