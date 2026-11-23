package auditsecurity

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (w *worker) captureRelayIPs(ctx context.Context, cfg operation_setting.AuditSecuritySetting) error {
	written := 0
	for range 200 {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, ok := model.TakeSecurityRelayIP()
		if !ok {
			return nil
		}
		if !cfg.Enabled || !cfg.IPIntelligenceEnabled || !cfg.RelayIPEnabled || cfg.Suspended {
			model.SecurityRelayIPDropped.Add(1)
			continue
		}
		accepted, err := w.observeRelayIP(ctx, entry)
		if err != nil {
			return err
		}
		if accepted {
			written++
		}
		if written == 5 {
			return nil
		}
	}
	return nil
}

// Actor role is the collection-time snapshot, never authority to punish an
// account or a claim about the role at the time an AI request began.
func (w *worker) observeRelayIP(ctx context.Context, entry model.SecurityRelayIP) (bool, error) {
	now := time.Now().Unix()
	address, err := netip.ParseAddr(entry.IP)
	if entry.UserID <= 0 || entry.QueuedAt < now-120 || err != nil {
		model.SecurityRelayIPDropped.Add(1)
		return false, nil
	}
	if !publicIPForIntelligence(entry.IP) {
		return false, nil
	}
	ip := address.Unmap().String()
	id := strings.TrimPrefix(model.SecurityAccountRef("relay-ip-v1:"+strconv.Itoa(entry.UserID)+":"+ip+":"+strconv.FormatInt(entry.At/600, 10)), "account:")
	if expiry := w.relayIPSeen[id]; expiry > now {
		return false, nil
	}
	if w.relayIPSeen == nil {
		w.relayIPSeen = make(map[string]int64)
	}
	if len(w.relayIPSeen) >= 10000 {
		for key, expiry := range w.relayIPSeen {
			if expiry <= now {
				delete(w.relayIPSeen, key)
			}
		}
		if len(w.relayIPSeen) >= 10000 {
			model.SecurityRelayIPDropped.Add(1)
			return false, nil
		}
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var user struct {
		ID, Role int
		Employee bool
	}
	err = w.db.WithContext(writeCtx).Table("users AS u").Select("u.id,u.role,EXISTS (SELECT 1 FROM employee_profiles AS ep WHERE ep.user_id = u.id AND ep.status = 1) AS employee").Where("u.id = ? AND u.deleted_at IS NULL", entry.UserID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		model.SecurityRelayIPDropped.Add(1)
		return false, nil
	}
	if err != nil {
		model.SecurityRelayIPDropped.Add(1)
		return false, err
	}
	e := model.AuditLog{EventId: id, UserId: user.ID, ActorRole: user.Role, ActorIsEmployee: &user.Employee, CreatedAt: now, Category: model.AuditCategorySecurity, Action: "relay.ip_observed", Success: true, Ip: ip}
	row, err := model.AuditLogStorageRow(w.writer, e)
	if err == nil {
		err = w.writer.WithContext(writeCtx).Table("audit_logs").Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error
	}
	if err != nil {
		model.SecurityRelayIPDropped.Add(1)
		return false, err
	}
	w.relayIPSeen[id] = now + 600
	model.SecurityRelayIPLastObserved.Store(now)
	return true, nil
}
