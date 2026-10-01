package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// These four run on the relay retry path for every attempt of every request,
// including requests that never configured any of the new settings.
func BenchmarkResolveRetryTimes(b *testing.B) {
	_ = operation_setting.UpdateGroupRetryTimesByJSONString(`{"cheap":5,"official":-1}`)
	defer func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(`{}`) }()

	b.Run("group configured", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = ResolveRetryTimes(0, "cheap", 1)
		}
	})
	b.Run("group absent (common)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = ResolveRetryTimes(0, "default", 1)
		}
	})
	b.Run("user override (short-circuit)", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = ResolveRetryTimes(3, "cheap", 1)
		}
	})
}

func BenchmarkShouldAttemptRelay(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ShouldAttemptRelay(1, 3, 2, 6)
	}
}

func BenchmarkRelayRetryBudgetExhausted(b *testing.B) {
	gin.SetMode(gin.TestMode)

	b.Run("no controller (feature off)", func(b *testing.B) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = RelayRetryBudgetExhausted(c)
		}
	})

	b.Run("with total deadline", func(b *testing.B) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl,
			benchDeadlineStub{total: time.Now().Add(10 * time.Minute)})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = RelayRetryBudgetExhausted(c)
		}
	})
}

type benchDeadlineStub struct{ total time.Time }

func (s benchDeadlineStub) MarkResponse(bool) bool { return false }
func (s benchDeadlineStub) RelayTimeoutDeadline() (time.Time, bool) {
	return s.total, !s.total.IsZero()
}
func (s benchDeadlineStub) RelayTotalDeadline() (time.Time, bool) {
	return s.total, !s.total.IsZero()
}

// Parallel stress over the shared state the retry path touches: the group quota
// RWMap and the hot config snapshot are read from every relay goroutine at once.
// Without -race available locally this at least surfaces crashes and torn reads.
func BenchmarkRetryPathParallel(b *testing.B) {
	_ = operation_setting.UpdateGroupRetryTimesByJSONString(`{"cheap":5,"official":-1}`)
	defer func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(`{}`) }()
	gin.SetMode(gin.TestMode)

	b.RunParallel(func(pb *testing.PB) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl,
			benchDeadlineStub{total: time.Now().Add(10 * time.Minute)})
		common.SetContextKey(c, constant.ContextKeyUserRetryTimes, 0)
		for pb.Next() {
			quota := ResolveRetryTimesForRequest(c, "cheap", 1)
			if quota < 0 {
				b.Fatalf("negative quota %d", quota)
			}
			if !ShouldAttemptRelay(0, quota, 0, 6) {
				b.Fatal("first attempt must always be allowed")
			}
			if _, _ = RelayRetryBudgetExhausted(c); false {
				b.Fatal("unreachable")
			}
		}
	})
}

// Concurrent writers to the group quota map while readers resolve, which is what
// happens when an admin saves the setting under load.
func BenchmarkRetryQuotaReadUnderWrite(b *testing.B) {
	gin.SetMode(gin.TestMode)
	stop := make(chan struct{})
	go func() {
		toggle := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			if toggle {
				_ = operation_setting.UpdateGroupRetryTimesByJSONString(`{"cheap":5}`)
			} else {
				_ = operation_setting.UpdateGroupRetryTimesByJSONString(`{"cheap":2}`)
			}
			toggle = !toggle
		}
	}()
	defer close(stop)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if q := ResolveRetryTimes(0, "cheap", 1); q < 0 {
				b.Fatalf("negative quota %d", q)
			}
		}
	})
}
