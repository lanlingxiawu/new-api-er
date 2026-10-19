package retryguard

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
)

//go:embed protection.lua
var protectionScript string
var protection = redis.NewScript(protectionScript)

type GroupStatus struct {
	Group                  string  `json:"group"`
	WindowSeconds          int     `json:"window_seconds"`
	RPM                    float64 `json:"rpm"`
	SlowCount              int64   `json:"slow_count"`
	Ready                  bool    `json:"ready"`
	RetryDisabledUntil     int64   `json:"retry_disabled_until"`
	CooldownLevel          int     `json:"cooldown_level"`
	CurrentCooldownSeconds int     `json:"current_cooldown_seconds"`
	serverTime             int64
}

func protectionArgs(rule operation_setting.GroupRetryGuardRule, sequence string, requests, slow int64) []interface{} {
	return []interface{}{rule.WindowSeconds, rule.RPMThreshold, rule.SlowCountThreshold, rule.CooldownSeconds, rule.CooldownMultiplier, rule.MaxCooldownSeconds, rule.ResetAfterSeconds, sequence, requests, slow}
}

func parseStatus(value interface{}, rule operation_setting.GroupRetryGuardRule) (GroupStatus, error) {
	values, ok := value.([]interface{})
	if !ok || len(values) != 7 {
		return GroupStatus{}, fmt.Errorf("invalid retry protection result")
	}
	var n [7]int64
	for i, v := range values {
		var valid bool
		n[i], valid = v.(int64)
		if !valid {
			return GroupStatus{}, fmt.Errorf("invalid retry protection counter")
		}
	}
	return GroupStatus{WindowSeconds: rule.WindowSeconds, serverTime: n[0], RPM: float64(n[1]) * 60 / float64(rule.WindowSeconds), SlowCount: n[2], RetryDisabledUntil: n[3], CooldownLevel: int(n[4]), CurrentCooldownSeconds: int(n[5]), Ready: n[6] == 1}, nil
}

// updateGroup provides the same atomic operation for isolated integration tests
// and callers without a pipeline. Production batches EvalSha on the worker.
func updateGroup(ctx context.Context, client *redis.Client, key, node, sequence string, rule operation_setting.GroupRetryGuardRule, requests, slow int64) (GroupStatus, error) {
	value, err := protection.Run(ctx, client, []string{key, key + ":node:" + node}, protectionArgs(rule, sequence, requests, slow)...).Result()
	if err != nil {
		return GroupStatus{}, err
	}
	return parseStatus(value, rule)
}
