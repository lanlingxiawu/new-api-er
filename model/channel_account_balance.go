package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const channelAccountBalanceKeyPrefix = "channel_account_balance:"
const channelAccountBalanceTTL = 7 * 24 * time.Hour

type ChannelAccountBalance struct {
	Group       string `json:"group"`
	Quota       int64  `json:"quota"`
	UsedQuota   int64  `json:"used_quota"`
	UpdatedTime int64  `json:"updated_time"`
}

func channelAccountBalanceKey(id int) string {
	return fmt.Sprintf("%s%d", channelAccountBalanceKeyPrefix, id)
}

func SetChannelAccountBalance(id int, data ChannelAccountBalance) error {
	if !common.RedisEnabled {
		return nil
	}
	b, err := common.Marshal(data)
	if err != nil {
		return err
	}
	return common.RDB.Set(context.Background(), channelAccountBalanceKey(id), string(b), channelAccountBalanceTTL).Err()
}

func GetChannelAccountBalance(id int) (*ChannelAccountBalance, error) {
	if !common.RedisEnabled {
		return nil, nil
	}
	val, err := common.RDB.Get(context.Background(), channelAccountBalanceKey(id)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var data ChannelAccountBalance
	if err := common.Unmarshal([]byte(val), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func DeleteChannelAccountBalance(id int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RDB.Del(context.Background(), channelAccountBalanceKey(id)).Err()
}

func BatchGetChannelAccountBalance(ids []int) map[int]*ChannelAccountBalance {
	result := make(map[int]*ChannelAccountBalance)
	if !common.RedisEnabled || len(ids) == 0 {
		return result
	}
	ctx := context.Background()
	pipe := common.RDB.Pipeline()
	cmds := make(map[int]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, channelAccountBalanceKey(id))
	}
	_, _ = pipe.Exec(ctx)
	for id, cmd := range cmds {
		val, err := cmd.Result()
		if err != nil {
			continue
		}
		var data ChannelAccountBalance
		if err := common.Unmarshal([]byte(val), &data); err != nil {
			continue
		}
		result[id] = &data
	}
	return result
}
