//go:build audit_uncommitted

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUncommittedAuditAbsorbedCostMustNotDependOnRetailGroup(t *testing.T) {
	c, info, _ := nonStreamTimeoutContext(t, NonStreamTimeoutBillingRefund, 0)
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	_, ordinary := nonStreamTimeoutPlan(c, info)
	info.PriceData.GroupRatioInfo.GroupRatio = 10
	_, markedUp := nonStreamTimeoutPlan(c, info)
	assert.Equal(t, ordinary["absorbed_quota_min"], markedUp["absorbed_quota_min"],
		"same upstream, usage, model price and cost factor: retail markup cannot multiply the platform's upstream cost")
}
