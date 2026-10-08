package model

import "gorm.io/gorm"

// Per-channel rows removed together with the channel.
//
// A channel's cost config (channel_cost_configs) goes with it: every delete path
// (single, batch, by status, disabled) deletes the configs of exactly the ids it
// deletes, in the same transaction, so a failed delete leaves both in place and
// a successful one leaves no orphan. (Abilities are deleted by the single and
// batch paths; the by-status paths leave them as upstream does.)
//
// Usage counters that the relay accumulators may still be writing
// (channel_daily_usage, channel_limit_period_usage) are removed after the commit
// by finalizeChannelDeletion; a late accumulator flush can recreate a row, which
// the periodic orphan sweep removes.
//
// History is kept: consumption_costs and employee_commission_logs (the ledger,
// each row carries its own cost ratio), platform_channel_daily_stats (receives
// the channel name snapshot) and channel_veridrop_detections (detection
// results, with their own retention).
//
// The cost ratio cache (L1 memory and Redis channel_cost_ratio:<id>) is left to
// expire by its TTL instead of being invalidated: requests that were already
// running on the channel settle after the delete, and while the ratio is still
// cached they are costed at the configured ratio. A settlement that misses the
// cache reads no row and uses the default 1.0, as for any unconfigured channel.

// deleteChannelCostConfigsTx deletes the cost configs of the given channels.
func deleteChannelCostConfigsTx(tx *gorm.DB, channelIds []int) error {
	if len(channelIds) == 0 {
		return nil
	}
	return tx.Where("channel_id IN ?", channelIds).Delete(&ChannelCostConfig{}).Error
}
