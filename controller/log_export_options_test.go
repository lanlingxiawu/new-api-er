package controller

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestExportUserOptionsSearchIdentity(t *testing.T) {
	employeeExportFilterTestDB(t)
	employee, customer, _ := employeeControllerExportFixture(t)
	other := mkUser(t, nil)
	var owned model.User
	require.NoError(t, model.DB.First(&owned, customer).Error)
	for _, tc := range []struct {
		path, keyword string
		want          int
	}{
		{"/api/log/export/options", strconv.Itoa(other.Id), other.Id},
		{"/api/log/export/options", other.Username, other.Id},
		{"/api/user/employee/export/options", strconv.Itoa(customer), customer},
		{"/api/user/employee/export/options", owned.Username, customer},
		{"/api/user/employee/export/options", strconv.Itoa(other.Id), 0},
		{"/api/user/employee/export/options", other.Username, 0},
		{"/api/log/export/options", "not-a-user-" + strconv.Itoa(other.Id), 0},
	} {
		t.Run(tc.path+"/"+tc.keyword, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodGet, tc.path+"?field=customer&keyword="+url.QueryEscape(tc.keyword), nil)
			GetLogExportFilterOptions(asUser(ctx, employee))
			response := decodeResp(t, rec)
			require.True(t, response.Success)
			var data struct{ Items []model.LogExportFilterOption }
			require.NoError(t, common.Unmarshal(response.Data, &data))
			if tc.want == 0 {
				require.Empty(t, data.Items)
				return
			}
			require.Len(t, data.Items, 1)
			require.Equal(t, strconv.Itoa(tc.want), data.Items[0].Value)
		})
	}
}
