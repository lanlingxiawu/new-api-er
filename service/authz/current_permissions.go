package authz

import (
	"context"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// ReadCurrentPermissions is for management PAT requests only. It deliberately
// reads the primary database instead of the asynchronously refreshed enforcer.
func ReadCurrentPermissions(ctx context.Context, userID, role int, permissions []Permission) (map[Permission]bool, error) {
	return ReadCurrentPermissionsFromDB(ctx, model.DB, userID, role, permissions)
}

// ReadCurrentPermissionsFromDB allows background readers to use an isolated pool.
func ReadCurrentPermissionsFromDB(ctx context.Context, db *gorm.DB, userID, role int, permissions []Permission) (map[Permission]bool, error) {
	result := make(map[Permission]bool, len(permissions))
	resources := make([]string, 0, len(permissions))
	for _, p := range permissions {
		if !slices.Contains(resources, p.Resource) {
			resources = append(resources, p.Resource)
		}
	}
	var rules []model.CasbinRule
	userSubject, roleSubject := UserSubject(userID), RoleSubject(BuiltInRoleAdmin)
	err := db.WithContext(ctx).Select("v0", "v1", "v2", "v3", "v4", "v5").Where("ptype = ? AND v0 IN ? AND v1 IN ?", "p", []string{userSubject, roleSubject}, resources).Find(&rules).Error
	if err != nil {
		return nil, err
	}
	for _, p := range permissions {
		if !isKnownPermission(p) {
			continue
		}
		if p.Resource == ResourceAdminAccessToken {
			if role != common.RoleAdminUser {
				continue
			}
			allow, deny := false, false
			for _, r := range rules {
				if r.V0 != userSubject || r.V1 != p.Resource || r.V2 != p.Action {
					continue
				}
				if r.V3 == EffectAllow && r.V4 == "" && r.V5 == "" {
					allow = true
				} else {
					deny = true
				}
			}
			result[p] = allow && !deny
			continue
		}
		if role == common.RoleRootUser {
			result[p] = true
			continue
		}
		if role != common.RoleAdminUser {
			continue
		}
		for _, subject := range []string{userSubject, roleSubject} {
			allow, deny := false, false
			for _, r := range rules {
				if r.V0 != subject || r.V1 != p.Resource || r.V2 != p.Action {
					continue
				}
				// Match the adapter's fail-closed handling of legacy own/other
				// constraints; they cannot become unrestricted grants here.
				effect, valid := legacyPolicyEffect(r.V3, r.V4, r.V5)
				if !valid || effect == EffectDeny {
					deny = true
				} else {
					allow = true
				}
			}
			if allow || deny {
				result[p] = allow && !deny
				break
			}
		}
	}
	return result, nil
}
