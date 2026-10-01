// Regression test (branch audit M2): GET /api/group/:group/models must not be
// reachable anonymously. Served publicly it let anyone enumerate hidden groups
// and which of their models had paid traffic in the last 30 minutes, and each
// call queried the logs table. It now sits behind UserAuth and the handler
// limits non-admins to groups they may use.

package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBranchAuditRegressionGroupModelsRouteRequiresAuth(t *testing.T) {
	found := false
	for _, chain := range collectRouteChains(t) {
		if chain.path != "/api/group/:group/models" {
			continue
		}
		found = true
		assert.GreaterOrEqual(t, chainIndex(chain.names, auditEnforcingAuth), 0,
			"%s %s is reachable anonymously: %v", chain.method, chain.path, chain.names)
	}
	assert.True(t, found, "route no longer registered")
}
