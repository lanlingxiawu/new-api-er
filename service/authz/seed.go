package authz

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	veridropMenuDenyMigrationKey       = "authz.migration.veridrop_menu_deny_v1"
	priceMonitorPermissionMigrationKey = "authz.migration.price_monitor_permission_v1"
)

func migrateVeridropMenuDenies(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var markerCount int64
		if err := tx.Model(&model.CasbinRule{}).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", veridropMenuDenyMigrationKey, ResourceAdminMenuVeridropDetection, ActionView, EffectAllow,
		).Count(&markerCount).Error; err != nil {
			return err
		}
		if markerCount > 0 {
			return nil
		}

		var legacyDenies []model.CasbinRule
		if err := tx.Where(
			"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", ResourceAdminMenuChannels, ActionView, EffectDeny,
		).Find(&legacyDenies).Error; err != nil {
			return err
		}
		for _, legacy := range legacyDenies {
			if !strings.HasPrefix(legacy.V0, "user:") {
				continue
			}
			var existing int64
			if err := tx.Model(&model.CasbinRule{}).Where(
				"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?",
				"p", legacy.V0, ResourceAdminMenuVeridropDetection, ActionView,
			).Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				continue
			}
			deny := newRule("p", []string{legacy.V0, ResourceAdminMenuVeridropDetection, ActionView, EffectDeny})
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&deny).Error; err != nil {
				return err
			}
		}
		marker := newRule("p", []string{veridropMenuDenyMigrationKey, ResourceAdminMenuVeridropDetection, ActionView, EffectAllow})
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error
	})
}

func migratePriceMonitorPermissions(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var markerCount int64
		if err := tx.Model(&model.CasbinRule{}).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", priceMonitorPermissionMigrationKey, ResourceAdminMenuPriceMonitor, ActionView, EffectAllow,
		).Count(&markerCount).Error; err != nil {
			return err
		}
		if markerCount > 0 {
			return nil
		}

		var legacyRules []model.CasbinRule
		if err := tx.Where(
			"ptype = ? AND v1 = ? AND v2 IN ?",
			"p", SystemSettingsResource("billing.model-pricing"), []string{ActionView, ActionEdit},
		).Find(&legacyRules).Error; err != nil {
			return err
		}
		for _, legacy := range legacyRules {
			if !strings.HasPrefix(legacy.V0, "user:") {
				continue
			}
			if legacy.V2 == ActionEdit && legacy.V3 == EffectAllow {
				var explicitViewDeny int64
				if err := tx.Model(&model.CasbinRule{}).Where(
					"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
					"p", legacy.V0, ResourceAdminMenuPriceMonitor, ActionView, EffectDeny,
				).Count(&explicitViewDeny).Error; err != nil {
					return err
				}
				if explicitViewDeny > 0 {
					continue
				}
			}
			var existing int64
			if err := tx.Model(&model.CasbinRule{}).Where(
				"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?",
				"p", legacy.V0, ResourceAdminMenuPriceMonitor, legacy.V2,
			).Count(&existing).Error; err != nil {
				return err
			}
			if existing == 0 {
				rule := newRule("p", []string{legacy.V0, ResourceAdminMenuPriceMonitor, legacy.V2, legacy.V3})
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rule).Error; err != nil {
					return err
				}
			}
			if legacy.V2 != ActionEdit || legacy.V3 != EffectAllow {
				continue
			}
			var viewRuleCount int64
			if err := tx.Model(&model.CasbinRule{}).Where(
				"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?",
				"p", legacy.V0, ResourceAdminMenuPriceMonitor, ActionView,
			).Count(&viewRuleCount).Error; err != nil {
				return err
			}
			if viewRuleCount == 0 {
				viewRule := newRule("p", []string{legacy.V0, ResourceAdminMenuPriceMonitor, ActionView, EffectAllow})
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&viewRule).Error; err != nil {
					return err
				}
			}
		}

		marker := newRule("p", []string{priceMonitorPermissionMigrationKey, ResourceAdminMenuPriceMonitor, ActionView, EffectAllow})
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error
	})
}

func seedBuiltInRoles(db *gorm.DB) error {
	for _, spec := range builtInRoles {
		role := model.AuthzRole{
			Key:         spec.Key,
			Name:        spec.Name,
			Description: spec.Description,
			BuiltIn:     spec.BuiltIn,
			Enabled:     true,
			Sort:        spec.Sort,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name",
				"description",
				"built_in",
				"enabled",
				"sort",
			}),
		}).Create(&role).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetBuiltInRolePolicies(db *gorm.DB) error {
	subjects := make([]string, 0, len(builtInRoles))
	for _, spec := range builtInRoles {
		subjects = append(subjects, RoleSubject(spec.Key))
	}
	return db.Where("ptype = ? AND v0 IN ?", "p", subjects).Delete(&model.CasbinRule{}).Error
}

func seedDefaultPolicies() error {
	e := currentEnforcer()
	if e == nil {
		return fmt.Errorf("authz enforcer is not initialized")
	}

	for _, spec := range builtInRoles {
		if spec.Superuser {
			continue
		}
		for _, permission := range PermissionsForRole(spec.Key) {
			if _, err := e.AddPolicy(RoleSubject(spec.Key), permission.Resource, permission.Action, EffectAllow); err != nil {
				return err
			}
		}
	}
	return nil
}
