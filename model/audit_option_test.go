package model

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuditOptionPersistenceFailureIsReported(t *testing.T) {
	if DB == nil {
		t.Skip("real database not configured")
	}
	key := fmt.Sprintf("audit_test_%d", time.Now().UnixNano())
	require.NoError(t, DB.Create(&Option{Key: key, Value: "before"}).Error)
	t.Cleanup(func() {
		DB.Where(&Option{Key: key}).Delete(&Option{})
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, key)
		common.OptionMapRWMutex.Unlock()
	})
	callback := "audit_test_reject_update"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if option, ok := tx.Statement.Dest.(*Option); ok && option.Key == key {
			tx.AddError(errors.New("test persistence rejected"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
	err := UpdateOption(key, "after")
	require.Error(t, err)
	var stored Option
	require.NoError(t, DB.Where(&Option{Key: key}).Take(&stored).Error)
	require.Equal(t, "before", stored.Value)
}
