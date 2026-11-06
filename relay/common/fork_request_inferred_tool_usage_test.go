package common

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestForkRequestInferredToolProvenance(t *testing.T) {
	for _, tc := range []struct {
		name               string
		inferred, reported bool
		vendor, items      int
		want, confirmed    int
	}{
		{name: "request alone", inferred: true, want: 1},
		{name: "vendor zero", inferred: true, reported: true},
		{name: "vendor one", inferred: true, reported: true, vendor: 1, want: 1, confirmed: 1},
		{name: "vendor two", inferred: true, reported: true, vendor: 2, want: 2, confirmed: 2},
		{name: "invalid vendor count", inferred: true, reported: true, vendor: -1, want: 1},
		{name: "protocol after request", inferred: true, items: 1, want: 2, confirmed: 1},
		{name: "protocol after vendor", inferred: true, reported: true, vendor: 2, items: 1, want: 3, confirmed: 3},
		{name: "protocol without inference", items: 1, want: 1, confirmed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const key = "fork_tool_provenance"
			info := &RelayInfo{WebSearchBillingKey: key}
			if tc.inferred {
				info.RecordRequestInferredToolCall(key)
			}
			if tc.reported {
				info.SetBillableToolCount(key, tc.vendor)
			}
			for i := 0; i < tc.items; i++ {
				info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			}
			tool := info.ResponsesUsageInfo.BuiltInTools[key]
			require.Equal(t, tc.want, tool.CallCount)
			require.Equal(t, tc.confirmed, tool.ConfirmedCallCount())
		})
	}
	var absent *BuildInToolInfo
	require.Zero(t, absent.ConfirmedCallCount())
	require.Zero(t, (&BuildInToolInfo{CallCount: -1, requestInferred: true}).ConfirmedCallCount())
}
