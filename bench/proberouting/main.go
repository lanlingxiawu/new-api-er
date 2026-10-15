// Command proberouting load-tests the real distribution middleware with the
// database from an explicit env file and loopback mock upstreams. It performs
// no migrations and never falls back to SQLite or invokes real AI providers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/tidwall/gjson"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type loadResult struct {
	Mode             string  `json:"mode"`
	TargetRPM        int     `json:"target_rpm"`
	ActualRPM        float64 `json:"actual_rpm"`
	Completed        int     `json:"completed"`
	Errors           int64   `json:"errors"`
	WrongRoutes      int64   `json:"wrong_routes"`
	Dropped          int     `json:"generator_dropped"`
	P50MS            float64 `json:"p50_ms"`
	P95MS            float64 `json:"p95_ms"`
	P99MS            float64 `json:"p99_ms"`
	ScheduledP99MS   float64 `json:"scheduled_p99_ms"`
	MaxMS            float64 `json:"max_ms"`
	DBQueries        int64   `json:"db_queries"`
	PolicyRefreshes  int64   `json:"policy_refreshes"`
	RequestDBQueries int64   `json:"request_db_queries"`
	DBWaitCount      int64   `json:"db_wait_count"`
	DBWaitMS         float64 `json:"db_wait_ms"`
	HeapMiB          float64 `json:"heap_mib"`
	AllocatedMiB     float64 `json:"allocated_mib"`
	GCCount          uint32  `json:"gc_count"`
	GCPauseMS        float64 `json:"gc_pause_ms"`
	Goroutines       int     `json:"goroutines"`
}

func main() {
	if err := run(); err != nil {
		// Errors are deliberately sanitized: driver errors can contain DSNs.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	envFile := flag.String("env-file", "", "Required existing project env file; SQL_DSN must be MySQL or PostgreSQL")
	duration := flag.Duration("duration", 30*time.Second, "Duration per mode/rate")
	workers := flag.Int("workers", 64, "Bounded request workers")
	output := flag.String("report", "probe-load-report.json", "JSON report path")
	flag.Parse()
	if *envFile == "" || *duration < time.Second || *duration > 5*time.Minute || *workers < 1 || *workers > 256 {
		return errors.New("provide -env-file; duration must be 1s..5m and workers 1..256")
	}
	config, err := godotenv.Read(*envFile)
	if err != nil {
		return errors.New("cannot read the specified env file")
	}
	dsn := config["SQL_DSN"]
	if dsn == "" || strings.HasPrefix(dsn, "sqlite") || strings.HasPrefix(dsn, "file:") {
		return errors.New("SQL_DSN must explicitly configure MySQL or PostgreSQL; SQLite is prohibited")
	}
	var dialector gorm.Dialector
	driver := "mysql"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") || strings.Contains(dsn, "host=") {
		driver = "postgres"
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	} else {
		parsed, parseErr := mysqlconfig.ParseDSN(dsn)
		if parseErr != nil {
			return errors.New("SQL_DSN is not a valid MySQL connection string")
		}
		parsed.ParseTime = true
		parsed.Timeout, parsed.ReadTimeout, parsed.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		dialector = mysql.Open(parsed.FormatDSN())
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		var mysqlError *mysqlconfig.MySQLError
		if errors.As(err, &mysqlError) {
			return fmt.Errorf("database connection failed (MySQL error %d); credentials have not been printed", mysqlError.Number)
		}
		return errors.New("database connection failed; credentials have not been printed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("database pool unavailable")
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	model.DB = db
	model.InitColumnNames()
	common.MemoryCacheEnabled = true
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	gin.SetMode(gin.ReleaseMode)
	if err := i18n.Init(); err != nil {
		return errors.New("i18n initialization failed")
	}
	var queries atomic.Int64
	if err := db.Callback().Query().After("gorm:query").Register("probe_load:count", func(*gorm.DB) { queries.Add(1) }); err != nil {
		return errors.New("cannot register query counter")
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 32*1024))
		if readErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if gjson.GetBytes(body, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"text\":\"ok\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"text":"ok"}`)
		}
	}))
	defer mock.Close()
	group := fmt.Sprintf("probe_load_%d", time.Now().UnixNano())
	channels := []*model.Channel{
		{Type: 1, Status: common.ChannelStatusEnabled, Name: group + "_a", Key: "local-mock-only", BaseURL: &mock.URL, Group: group, Models: group, Priority: common.GetPointer(int64(100))},
		{Type: 1, Status: common.ChannelStatusEnabled, Name: group + "_b", Key: "local-mock-only", BaseURL: &mock.URL, Group: group, Models: group, Priority: common.GetPointer(int64(0))},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, channel := range channels {
			if err := tx.Create(channel).Error; err != nil {
				return err
			}
			if err := channel.AddAbilities(tx); err != nil {
				return err
			}
		}
		return nil
	})
	cancel()
	if err != nil {
		return errors.New("cannot create isolated channel fixtures; no schema migration attempted")
	}
	ids := []int{channels[0].Id, channels[1].Id}
	fmt.Printf("CONFIG driver=%s pool_limit=4 fixture_group=%s channels=%v\n", driver, group, ids)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("channel_id IN ?", ids).Delete(&model.Ability{}).Error; err != nil {
				return err
			}
			return tx.Where("id IN ? AND name IN ?", ids, []string{channels[0].Name, channels[1].Name}).Delete(&model.Channel{}).Error
		})
		var remainingChannels, remainingAbilities int64
		countErr := db.WithContext(ctx).Model(&model.Channel{}).Where("id IN ?", ids).Count(&remainingChannels).Error
		abilityErr := db.WithContext(ctx).Model(&model.Ability{}).Where("channel_id IN ?", ids).Count(&remainingAbilities).Error
		if err != nil || countErr != nil || abilityErr != nil || remainingChannels != 0 || remainingAbilities != 0 {
			runErr = fmt.Errorf("cleanup verification failed; inspect only fixture channel IDs %v", ids)
		} else {
			fmt.Println("CLEANUP channels=0 abilities=0")
		}
	}()
	transport := &http.Transport{MaxIdleConns: *workers * 4, MaxIdleConnsPerHost: *workers * 2, MaxConnsPerHost: *workers * 2}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var calibrationCompleted, calibrationErrors atomic.Int64
	var calibrationWorkers sync.WaitGroup
	calibrationStart := clockNS()
	calibrationEnd := calibrationStart + int64(3*time.Second)
	for worker := 0; worker < *workers; worker++ {
		calibrationWorkers.Add(1)
		go func() {
			defer calibrationWorkers.Done()
			for clockNS() < calibrationEnd {
				response, err := client.Post(mock.URL, "application/json", strings.NewReader(`{"input":"hi","stream":true}`))
				if err != nil {
					calibrationErrors.Add(1)
					continue
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if readErr != nil || response.StatusCode != 200 {
					calibrationErrors.Add(1)
				}
				calibrationCompleted.Add(1)
			}
		}()
	}
	calibrationWorkers.Wait()
	calibrationRPS := float64(calibrationCompleted.Load()) / float64(clockNS()-calibrationStart) * 1e9
	fmt.Printf("CALIBRATION mock_rps=%.1f errors=%d\n", calibrationRPS, calibrationErrors.Load())
	if calibrationErrors.Load() != 0 || calibrationRPS < 5001 {
		return errors.New("mock baseline below 3x target throughput or returned errors")
	}
	var wrongRoutes atomic.Int64
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		defer common.CleanupBodyStorage(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		c.Next()
	}, middleware.Distribute())
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		engine.POST(path, func(c *gin.Context) {
			expected, _ := strconv.Atoi(c.GetHeader("X-Expected-Channel"))
			selected := c.GetInt("channel_id")
			if selected != expected || (selected != ids[0] && selected != ids[1]) {
				wrongRoutes.Add(1)
				c.Status(http.StatusInternalServerError)
				return
			}
			storage, err := common.GetBodyStorage(c)
			if err != nil {
				c.Status(500)
				return
			}
			body, err := storage.Bytes()
			if err != nil {
				c.Status(500)
				return
			}
			// Always send to this process's loopback mock, never an existing channel.
			request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, mock.URL, strings.NewReader(string(body)))
			if err != nil {
				c.Status(500)
				return
			}
			response, err := client.Do(request)
			if err != nil {
				c.Status(502)
				return
			}
			defer response.Body.Close()
			c.Header("Content-Type", response.Header.Get("Content-Type"))
			c.Header("X-Selected-Channel", strconv.Itoa(selected))
			c.Status(response.StatusCode)
			_, _ = io.Copy(c.Writer, response.Body)
		})
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	results := make([]loadResult, 0, 8)
	for _, rate := range []int{500, 1667} {
		for _, mode := range []string{"disabled", "all", "non_stream", "stream"} {
			settings := map[string]any{"disable_probe_requests": mode != "disabled", "probe_request_mode": mode}
			if mode == "disabled" {
				settings["probe_request_mode"] = "all"
			}
			raw, err := common.Marshal(settings)
			if err != nil {
				return errors.New("cannot encode fixture settings")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = db.WithContext(ctx).Model(&model.Channel{}).Where("id = ? AND name = ?", ids[0], channels[0].Name).Update("settings", string(raw)).Error
			cancel()
			if err != nil {
				return errors.New("cannot update isolated fixture settings")
			}
			model.InitChannelCache()
			policy := model.CurrentChannelProbePolicy()
			for _, stream := range []bool{false, true} {
				want := mode == "all" || mode == "stream" && stream || mode == "non_stream" && !stream
				if policy.ForStream(stream).Blocks(ids[0]) != want {
					return errors.New("policy refresh did not apply fixture settings")
				}
			}
			total := int(*duration) * rate / int(time.Second)
			latency := make([]int64, total)
			scheduled := make([]int64, total)
			jobs := make(chan int, *workers*2)
			var errorsCount atomic.Int64
			var wg sync.WaitGroup
			wrongBefore, queryBefore := wrongRoutes.Load(), queries.Load()
			poolBefore := sqlDB.Stats()
			var memoryBefore runtime.MemStats
			runtime.ReadMemStats(&memoryBefore)
			begin := clockNS()
			stopRefresh, refreshDone := make(chan struct{}), make(chan struct{})
			var refreshCount atomic.Int64
			go func() {
				defer close(refreshDone)
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-stopRefresh:
						return
					case <-ticker.C:
						model.RefreshChannelProbePolicy()
						refreshCount.Add(1)
					}
				}
			}()
			for worker := 0; worker < *workers; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for index := range jobs {
						stream := index%2 == 1
						short := (index/2)%4 != 0
						text := "hi"
						if !short {
							text = strings.Repeat("x", 256)
						}
						path := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}[index%3]
						body := map[string]any{"model": group, "stream": stream}
						if path == "/v1/responses" {
							body["input"] = text
						} else {
							body["messages"] = []map[string]string{{"role": "user", "content": text}}
						}
						raw, _ := common.Marshal(body)
						request, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(string(raw)))
						want := ids[0]
						if short && (mode == "all" || mode == "stream" && stream || mode == "non_stream" && !stream) {
							want = ids[1]
						}
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("X-Expected-Channel", strconv.Itoa(want))
						start := clockNS()
						response, err := client.Do(request)
						if err != nil {
							errorsCount.Add(1)
						} else {
							responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 32*1024))
							response.Body.Close()
							if readErr != nil || response.StatusCode != 200 || response.Header.Get("X-Selected-Channel") != strconv.Itoa(want) || (stream && !strings.Contains(string(responseBody), "[DONE]")) {
								errorsCount.Add(1)
							}
						}
						end := clockNS()
						latency[index] = end - start
						scheduled[index] = end - begin - int64(index)*int64(time.Second)/int64(rate)
					}
				}()
			}
			dropped := 0
			for index := 0; index < total; index++ {
				due := begin + int64(index)*int64(time.Second)/int64(rate)
				if delay := due - clockNS(); delay > 0 {
					time.Sleep(time.Duration(delay))
				}
				select {
				case jobs <- index:
				default:
					dropped++
				}
			}
			close(jobs)
			wg.Wait()
			elapsed := time.Duration(clockNS() - begin)
			close(stopRefresh)
			<-refreshDone
			var memoryAfter runtime.MemStats
			runtime.ReadMemStats(&memoryAfter)
			poolAfter := sqlDB.Stats()
			sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
			sort.Slice(scheduled, func(i, j int) bool { return scheduled[i] < scheduled[j] })
			latency, scheduled = latency[dropped:], scheduled[dropped:]
			if len(latency) == 0 {
				return errors.New("load generator completed no requests")
			}
			result := loadResult{Mode: mode, TargetRPM: rate * 60, ActualRPM: float64(total-dropped) / elapsed.Seconds() * 60, Completed: total - dropped,
				Errors: errorsCount.Load(), WrongRoutes: wrongRoutes.Load() - wrongBefore, Dropped: dropped,
				P50MS: float64(latency[len(latency)*50/100]) / 1e6, P95MS: float64(latency[len(latency)*95/100]) / 1e6,
				P99MS: float64(latency[len(latency)*99/100]) / 1e6, ScheduledP99MS: float64(scheduled[len(scheduled)*99/100]) / 1e6, MaxMS: float64(latency[len(latency)-1]) / 1e6,
				DBQueries: queries.Load() - queryBefore, DBWaitCount: poolAfter.WaitCount - poolBefore.WaitCount, DBWaitMS: float64(poolAfter.WaitDuration-poolBefore.WaitDuration) / float64(time.Millisecond),
				PolicyRefreshes: refreshCount.Load(), RequestDBQueries: queries.Load() - queryBefore - refreshCount.Load(),
				HeapMiB: float64(memoryAfter.HeapAlloc) / 1048576, AllocatedMiB: float64(memoryAfter.TotalAlloc-memoryBefore.TotalAlloc) / 1048576,
				GCCount: memoryAfter.NumGC - memoryBefore.NumGC, GCPauseMS: float64(memoryAfter.PauseTotalNs-memoryBefore.PauseTotalNs) / 1e6, Goroutines: runtime.NumGoroutine()}
			results = append(results, result)
			encoded, _ := common.Marshal(result)
			fmt.Println("RESULT", string(encoded))
			report, _ := common.Marshal(map[string]any{"scope": "distribution middleware with loopback mock; excludes authentication, billing, and provider adapters", "driver": driver, "duration_seconds": duration.Seconds(), "workers": *workers, "stream_ratio": 0.5, "probe_ratio": 0.75, "mock_baseline_rps": calibrationRPS, "refresh_interval_seconds": 10, "results": results})
			if err := os.WriteFile(*output, report, 0600); err != nil {
				return errors.New("cannot write report")
			}
			if result.Errors != 0 || result.WrongRoutes != 0 || result.Dropped != 0 {
				return errors.New("load test failed; see the report; stopping further load")
			}
		}
	}
	return nil
}
