package model

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

var ErrProbeChannelUnavailable = errors.New("no channel accepts probe requests in this group")

// ChannelProbePolicy is immutable after publication. A request holds a single
// version across initial selection and retries.
type ChannelProbePolicy struct {
	blocked     map[int]bool
	refreshedAt time.Time
}

func (p *ChannelProbePolicy) Blocks(id int) bool {
	return p != nil && p.blocked[id]
}

var channelProbePolicy atomic.Pointer[ChannelProbePolicy]
var channelProbeRefresh sync.Mutex

func CurrentChannelProbePolicy() *ChannelProbePolicy {
	policy := channelProbePolicy.Load()
	if policy == nil || len(policy.blocked) == 0 || time.Since(policy.refreshedAt) > 3*time.Minute {
		return nil
	}
	return policy
}

// RefreshChannelProbePolicy is called only on management/background paths.
// Serialize refreshes outside the relay's shared locks to prevent stale reads
// publishing over a newer admin save. Requests only load the atomic snapshot.
func RefreshChannelProbePolicy() {
	channelProbeRefresh.Lock()
	defer channelProbeRefresh.Unlock()
	defer func() {
		if recover() != nil {
			common.SysError("channel probe policy refresh panicked; retaining previous snapshot")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var channels []struct {
		Id       int
		Settings string
	}
	if err := DB.WithContext(ctx).Table("channels").Select("id, settings").Find(&channels).Error; err != nil {
		common.SysError("channel probe policy refresh failed: " + err.Error())
		return
	}
	next := &ChannelProbePolicy{blocked: make(map[int]bool), refreshedAt: time.Now()}
	for _, channel := range channels {
		if channel.Settings == "" {
			continue
		}
		var settings struct {
			DisableProbeRequests bool `json:"disable_probe_requests"`
		}
		if err := common.UnmarshalJsonStr(channel.Settings, &settings); err != nil {
			common.SysError("invalid channel probe policy; retaining previous snapshot")
			return
		}
		if settings.DisableProbeRequests {
			next.blocked[channel.Id] = true
		}
	}
	channelProbePolicy.Store(next)
}

func SyncChannelProbePolicy() {
	RefreshChannelProbePolicy()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		RefreshChannelProbePolicy()
	}
}

// PreserveProbeSetting keeps legacy clients from clearing a configured policy.
// Explicit false is preserved. Other settings keep their existing update semantics.
func (channel *Channel) PreserveProbeSetting(origin *Channel) error {
	if channel.OtherSettings == "" && origin != nil {
		channel.OtherSettings = origin.OtherSettings
		return nil
	}
	values := make(map[string]any)
	if channel.OtherSettings != "" {
		if err := common.UnmarshalJsonStr(channel.OtherSettings, &values); err != nil {
			return err
		}
	}
	if value, exists := values["disable_probe_requests"]; exists {
		if _, valid := value.(bool); !valid {
			return errors.New("invalid probe setting")
		}
		return nil
	}
	if origin == nil || !origin.GetOtherSettings().DisableProbeRequests {
		return nil
	}
	if values == nil {
		values = make(map[string]any)
	}
	values["disable_probe_requests"] = true
	raw, err := common.Marshal(values)
	if err == nil {
		channel.OtherSettings = string(raw)
	}
	return err
}
