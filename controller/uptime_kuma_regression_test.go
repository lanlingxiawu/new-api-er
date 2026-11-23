package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKumaUsesLatestHeartbeat(t *testing.T) {
	for _, testCase := range []struct {
		name, heartbeats string
		want             int
	}{{"down", `[{"status":1},{"status":0}]`, 0}, {"up", `[{"status":0},{"status":1}]`, 1}, {"single", `[{"status":1}]`, 1}, {"empty", `[]`, 0}} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/status-page/heartbeat/test" {
					_, err := writer.Write([]byte(`{"heartbeatList":{"1":` + testCase.heartbeats + `},"uptimeList":{"1_24":0.9}}`))
					require.NoError(t, err)
					return
				}
				_, err := writer.Write([]byte(`{"publicGroupList":[{"name":"group","monitorList":[{"id":1,"name":"monitor"}]}]}`))
				require.NoError(t, err)
			}))
			defer server.Close()
			result := fetchGroupData(context.Background(), server.Client(), map[string]any{"url": server.URL, "slug": "test"})
			require.Len(t, result.Monitors, 1)
			require.Equal(t, testCase.want, result.Monitors[0].Status)
			require.Equal(t, 0.9, result.Monitors[0].Uptime)
		})
	}
}
