package ratio_setting

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchAuditGroupRatioMatchesMainWhenDisabled(t *testing.T) {
	oldBase, oldSpecial := GroupRatio2JSONString(), GroupGroupRatio2JSONString()
	oldEnabled := IsUserExclusiveGroupRatioEnabled()
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupRatioByJSONString(oldBase))
		require.NoError(t, UpdateGroupGroupRatioByJSONString(oldSpecial))
		SetUserExclusiveGroupRatioEnabled(oldEnabled)
	})
	require.NoError(t, UpdateGroupRatioByJSONString(`{"audit-paid":2,"audit-free":0}`))
	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{"audit-vip":{"audit-paid":0.5,"audit-free":0}}`))
	SetUserExclusiveGroupRatioEnabled(false)
	for _, user := range []string{"audit-vip", "audit-standard"} {
		for _, group := range []string{"audit-paid", "audit-free"} {
			for _, exclusive := range []float64{0, 0.25, 10, -1, math.NaN(), math.Inf(1)} {
				t.Run(fmt.Sprintf("%s/%s/%g", user, group, exclusive), func(t *testing.T) {
					// main's original resolution: group-group, then base group.
					want, special := GetGroupGroupRatio(user, group)
					if !special {
						want = GetGroupRatio(group)
					}
					got, gotSpecial := ResolveGroupRatio(map[string]float64{group: exclusive}, user, group)
					assert.Equal(t, want, got)
					assert.Equal(t, special, gotSpecial)
				})
			}
		}
	}
}
