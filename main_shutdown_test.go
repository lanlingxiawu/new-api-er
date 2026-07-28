package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 关停必须先排空 HTTP 在途请求、再刷台账缓冲。
//
// 历史实现是反的：先 ShutdownStatsFlush（会永久关闭刷盘 goroutine），再 srv.Shutdown。
// 排空窗口内完成的请求把成本/提成记录写进缓冲后再无人落库，进程退出即静默丢失。
// 本用例在顺序被改回去时会失败。
func TestShutdownSequence_DrainsBeforeFlush(t *testing.T) {
	var order []string

	shutdownSequence(
		func() error {
			order = append(order, "drain")
			return nil
		},
		func() {
			order = append(order, "flush")
		},
	)

	require.Equal(t, []string{"drain", "flush"}, order)
}

// 排空报错（例如 SSE 流超出 SHUTDOWN_TIMEOUT_SECONDS 被强制关闭）不能吞掉刷盘。
// 这正是最需要落库的场景：有请求被砍断，缓冲里必然有未落库的台账。
func TestShutdownSequence_FlushesEvenWhenDrainFails(t *testing.T) {
	var order []string

	shutdownSequence(
		func() error {
			order = append(order, "drain")
			return errors.New("context deadline exceeded")
		},
		func() {
			order = append(order, "flush")
		},
	)

	require.Equal(t, []string{"drain", "flush"}, order)
}

// flush 必须无条件执行一次，不能因为 drain 返回 nil 就跳过。
func TestShutdownSequence_AlwaysFlushesOnce(t *testing.T) {
	flushCount := 0

	shutdownSequence(func() error { return nil }, func() { flushCount++ })

	assert.Equal(t, 1, flushCount)
}
