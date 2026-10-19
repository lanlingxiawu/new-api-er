// Package retryguard consumes existing monitoring samples. It never performs
// network I/O, parsing, or waits on a relay goroutine.
package retryguard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
)

type sample struct {
	group, revision string
	latency         int64
	completedAt     int64
}
type counters struct{ requests, slow int64 }
type groupView struct {
	GroupStatus
	until time.Time
}
type runtimeSnapshot struct {
	revision  string
	updatedAt time.Time
	groups    map[string]groupView
}
type engine struct {
	events   chan sample
	snapshot atomic.Pointer[runtimeSnapshot]
	dropped  atomic.Uint64
	running  atomic.Bool
}

var runtime = &engine{events: make(chan sample, 8192)}

type Status struct {
	Available      bool          `json:"available"`
	UpdatedAt      int64         `json:"updated_at"`
	DroppedSamples uint64        `json:"dropped_samples"`
	Groups         []GroupStatus `json:"groups"`
}

// Observe receives already computed latency. Negative latency still counts RPM
// but is not eligible for slow-response counting.
func Observe(group string, latency int64, completedAt int64) {
	policy := operation_setting.GroupRetryGuardSnapshot()
	if !policy.Enabled || !runtime.running.Load() {
		return
	}
	rule, ok := policy.Rules[group]
	if !ok || !rule.Enabled {
		return
	}
	select {
	case runtime.events <- sample{group: group, revision: policy.Revision, latency: latency, completedAt: completedAt}:
	default:
		runtime.dropped.Add(1)
	}
}

func Disabled(group string) bool { return runtime.disabled(group, time.Now()) }

func (e *engine) disabled(group string, now time.Time) bool {
	policy := operation_setting.GroupRetryGuardSnapshot()
	if !policy.Enabled {
		return false
	}
	rule, ok := policy.Rules[group]
	if !ok || !rule.Enabled {
		return false
	}
	snapshot := e.snapshot.Load()
	if snapshot == nil || snapshot.revision != policy.Revision || now.Sub(snapshot.updatedAt) > 5*time.Second {
		return false
	}
	return now.Before(snapshot.groups[group].until)
}

func GetStatus() Status {
	result := Status{Groups: []GroupStatus{}, DroppedSamples: runtime.dropped.Load()}
	policy := operation_setting.GroupRetryGuardSnapshot()
	snapshot := runtime.snapshot.Load()
	if snapshot == nil || snapshot.revision != policy.Revision || !policy.Enabled {
		return result
	}
	result.UpdatedAt = snapshot.updatedAt.Unix()
	result.Available = time.Since(snapshot.updatedAt) <= 5*time.Second
	for _, view := range snapshot.groups {
		result.Groups = append(result.Groups, view.GroupStatus)
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].Group < result.Groups[j].Group })
	return result
}

// Start owns one worker and an isolated two-connection Redis pool. It is
// idempotent; the returned stop waits only for the bounded background operation.
func Start() func() {
	if !runtime.running.CompareAndSwap(false, true) {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); defer runtime.running.Store(false); runtime.run(ctx) }()
	return func() { cancel(); <-done }
}

func (e *engine) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var client *redis.Client
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	pending := map[string]counters{}
	revision := ""
	node := common.GetUUID()
	sequence := uint64(0)
	loaded := false
	lastError := time.Time{}
	lastDropped := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-e.events:
			policy := operation_setting.GroupRetryGuardSnapshot()
			if revision != policy.Revision {
				pending = map[string]counters{}
				revision = policy.Revision
			}
			rule, ok := policy.Rules[event.group]
			if !policy.Enabled || !ok || !rule.Enabled || event.revision != policy.Revision || time.Now().Unix()-event.completedAt > 5 {
				continue
			}
			count := pending[event.group]
			count.requests++
			if event.latency >= rule.LatencyThresholdMs {
				count.slow++
			}
			pending[event.group] = count
		case <-ticker.C:
			policy := operation_setting.GroupRetryGuardSnapshot()
			if revision != policy.Revision {
				pending = map[string]counters{}
				revision = policy.Revision
			}
			if !policy.Enabled || !common.RedisEnabled || common.RDB == nil {
				pending = map[string]counters{}
				e.snapshot.Store(nil)
				if client != nil {
					_ = client.Close()
					client = nil
					loaded = false
				}
				continue
			}
			if client == nil {
				options := *common.RDB.Options()
				options.PoolSize = 2
				options.MinIdleConns = 0
				options.MaxRetries = -1
				options.PoolTimeout = 500 * time.Millisecond
				options.DialTimeout = 500 * time.Millisecond
				options.ReadTimeout = 500 * time.Millisecond
				options.WriteTimeout = 500 * time.Millisecond
				client = redis.NewClient(&options)
			}
			sequence++
			err := e.flush(ctx, client, policy, pending, node, strconv.FormatUint(sequence, 10), &loaded)
			// Failed batches are discarded rather than replayed with a new ID: a
			// timed-out Redis command may already have committed. Missing data degrades
			// protection; it must never manufacture extra slow requests.
			pending = map[string]counters{}
			dropped := e.dropped.Load()
			if (err != nil || dropped != lastDropped) && time.Since(lastError) >= time.Minute {
				if err != nil {
					common.SysError("group retry protection sync failed: " + err.Error())
				}
				if dropped != lastDropped {
					common.SysError(fmt.Sprintf("group retry protection dropped %d samples", dropped-lastDropped))
					lastDropped = dropped
				}
				lastError = time.Now()
			}
		}
	}
}

func (e *engine) flush(parent context.Context, client *redis.Client, policy *operation_setting.CompiledGroupRetryGuard, pending map[string]counters, node, sequence string, loaded *bool) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("worker panic: %v", recovered)
			*loaded = false
		}
	}()
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	if !*loaded {
		if _, err = protection.Load(ctx, client).Result(); err != nil {
			return err
		}
		*loaded = true
	}
	pipe := client.Pipeline()
	defer pipe.Close()
	commands := map[string]*redis.Cmd{}
	for group, rule := range policy.Rules {
		if !rule.Enabled {
			continue
		}
		digest := sha256.Sum256([]byte(group))
		key := fmt.Sprintf("group_retry_guard:v1:{%x}:%s", digest, policy.Revision)
		count := pending[group]
		commands[group] = protection.EvalSha(ctx, pipe, []string{key, key + ":node:" + node}, protectionArgs(rule, sequence, count.requests, count.slow)...)
	}
	if _, err = pipe.Exec(ctx); err != nil {
		*loaded = false
		return err
	}
	now := time.Now()
	snapshot := &runtimeSnapshot{revision: policy.Revision, updatedAt: now, groups: map[string]groupView{}}
	previous := e.snapshot.Load()
	for group, command := range commands {
		value, cmdErr := command.Result()
		if cmdErr != nil {
			return cmdErr
		}
		status, parseErr := parseStatus(value, policy.Rules[group])
		if parseErr != nil {
			return parseErr
		}
		status.Group = group
		view := groupView{GroupStatus: status, until: now.Add(time.Duration(status.RetryDisabledUntil-status.serverTime) * time.Second)}
		snapshot.groups[group] = view
		if previous != nil && previous.revision == policy.Revision {
			wasPaused := previous.groups[group].RetryDisabledUntil > previous.groups[group].serverTime
			paused := status.RetryDisabledUntil > status.serverTime
			if wasPaused != paused {
				common.SysLog(fmt.Sprintf("group retry protection transition group=%q paused=%t cooldown_seconds=%d", group, paused, status.CurrentCooldownSeconds))
			}
		}
	}
	e.snapshot.Store(snapshot)
	return nil
}
