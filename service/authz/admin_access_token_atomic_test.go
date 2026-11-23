package authz

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSetUserPermissionsAdminTokenAtomic(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })

	inserts := 0
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:admin_token_permission_atomicity", func(tx *gorm.DB) {
		if tx.Statement.Table != "casbin_rule" {
			return
		}
		inserts++
		if inserts == 2 {
			tx.AddError(errors.New("injected second permission failure"))
		}
	}))
	err := SetUserPermissions(42, PermissionsMap{ResourceAdminAccessToken: {
		ActionUse: true, ActionUsersList: true, ActionChannelsOwned: true,
	}})
	require.ErrorContains(t, err, "injected second permission failure")
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ?", UserSubject(42), ResourceAdminAccessToken).Count(&count).Error)
	require.Zero(t, count, "one failed action must roll back the complete management-token grant")
}
