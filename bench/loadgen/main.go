// Command loadgen 是针对 new-api 网关 /v1/chat/completions 的链路压测器。
//
// 特性：
//   - 闭环并发模型：-c 个 worker 持续发压至 -d 时长结束；
//   - 流式/非流式按 -stream-ratio 混合，SSE 流完整消费到 [DONE]；
//   - 统计：分形态的 p50/p90/p95/p99/max 延迟、流式 TTFB、状态码分布、错误采样；
//   - 每 5 秒输出实时 RPS。
//
// 仅压测用途，不属于业务代码。用法见 bench/README.md。
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var (
	target      = flag.String("url", "http://127.0.0.1:3000/v1/chat/completions", "目标接口")
	token       = flag.String("token", "", "API 令牌（sk-...）；直压 mock 做基线时可留空")
	model       = flag.String("model", "gpt-4o-mini", "请求模型名")
	conc        = flag.Int("c", 50, "并发 worker 数（闭环）")
	dur         = flag.Duration("d", 60*time.Second, "压测时长")
	streamRatio = flag.Float64("stream-ratio", 0.5, "流式请求占比 [0,1]")
	timeout     = flag.Duration("timeout", 120*time.Second, "单请求超时")
	maxTokens   = flag.Int("max-tokens", 256, "请求 max_tokens")
	promptWords = flag.Int("prompt-words", 30, "随机 prompt 词数")
	reportPath  = flag.String("report", "", "压测结束后将 JSON 报告写入该文件（留空不输出）")

	perfURL      = flag.String("perf-url", "", "网关性能接口，如 http://127.0.0.1:3000/api/performance/stats（留空不采样）")
	adminToken   = flag.String("admin-token", "", "root 用户的系统访问令牌（个人设置生成），用于 -perf-url 鉴权")
	adminUserID  = flag.String("admin-user-id", "", "root 用户 ID，用于性能接口 New-Api-User 鉴权头")
	perfInterval = flag.Duration("perf-interval", 5*time.Second, "性能采样间隔")
	reportZhPath = flag.String("report-zh", "", "压测结束后将中文 Markdown 报告写入该文件")
	mysqlDSN     = flag.String("mysql-dsn", "", "MySQL DSN；配置后报告包含数据库连接与线程采样")
	dbInterval   = flag.Duration("db-interval", 5*time.Second, "数据库采样间隔")
)

type sample struct {
	lat    float64 // ms，整请求耗时
	ttfb   float64 // ms，流式首字节（非流式为 0）
	stream bool
}

type workerStat struct {
	samples []sample
	codes   map[int]int64
	errs    []string
	errN    int64
	chunks  int64
}

var promptPool []string

func initPromptPool() {
	promptPool = make([]string, 256)
	for i := range promptPool {
		l := 3 + rand.IntN(6)
		b := make([]byte, l)
		for j := range b {
			b[j] = byte('a' + rand.IntN(26))
		}
		promptPool[i] = string(b)
	}
}

func buildBody(isStream bool) []byte {
	var b bytes.Buffer
	b.WriteString(`{"model":"`)
	b.WriteString(*model)
	b.WriteString(`","stream":`)
	if isStream {
		b.WriteString(`true,"stream_options":{"include_usage":true},`)
	} else {
		b.WriteString(`false,`)
	}
	fmt.Fprintf(&b, `"max_tokens":%d,"messages":[{"role":"user","content":"`, *maxTokens)
	for i := 0; i < *promptWords; i++ {
		b.WriteString(promptPool[rand.IntN(len(promptPool))])
		b.WriteByte(' ')
	}
	b.WriteString(`"}]}`)
	return b.Bytes()
}

func (s *workerStat) recordErr(msg string) {
	s.errN++
	if len(s.errs) < 3 {
		s.errs = append(s.errs, msg)
	}
}

func worker(client *http.Client, deadline time.Time, st *workerStat, done *atomic.Int64) {
	for time.Now().Before(deadline) {
		isStream := rand.Float64() < *streamRatio
		body := buildBody(isStream)
		req, err := http.NewRequest(http.MethodPost, *target, bytes.NewReader(body))
		if err != nil {
			st.recordErr("build request: " + err.Error())
			return
		}
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		req.Header.Set("Content-Type", "application/json")

		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			st.recordErr(err.Error())
			done.Add(1)
			continue
		}

		var ttfb float64
		if resp.StatusCode == http.StatusOK {
			if isStream {
				br := bufio.NewReaderSize(resp.Body, 16<<10)
				first := true
				for {
					line, rerr := br.ReadString('\n')
					if first && line != "" {
						ttfb = float64(time.Since(start)) / 1e6
						first = false
					}
					if strings.HasPrefix(line, "data:") && !strings.Contains(line, "[DONE]") {
						st.chunks++
					}
					if strings.Contains(line, "[DONE]") || rerr != nil {
						break
					}
				}
				_, _ = io.Copy(io.Discard, resp.Body)
			} else {
				_, _ = io.Copy(io.Discard, resp.Body)
			}
		} else {
			eb, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			st.recordErr(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(eb))))
		}
		_ = resp.Body.Close()

		st.codes[resp.StatusCode]++
		if resp.StatusCode == http.StatusOK {
			st.samples = append(st.samples, sample{
				lat:    float64(time.Since(start)) / 1e6,
				ttfb:   ttfb,
				stream: isStream,
			})
		}
		done.Add(1)
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// latencyStats 单个形态的延迟统计摘要（毫秒），用于终端输出与 JSON 报告。
type latencyStats struct {
	Count int     `json:"count"`
	AvgMs float64 `json:"avg_ms"`
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

func summarize(lats []float64) latencyStats {
	if len(lats) == 0 {
		return latencyStats{}
	}
	sort.Float64s(lats)
	var sum float64
	for _, v := range lats {
		sum += v
	}
	round := func(v float64) float64 { return float64(int(v*10)) / 10 }
	return latencyStats{
		Count: len(lats),
		AvgMs: round(sum / float64(len(lats))),
		P50Ms: round(percentile(lats, 0.50)),
		P90Ms: round(percentile(lats, 0.90)),
		P95Ms: round(percentile(lats, 0.95)),
		P99Ms: round(percentile(lats, 0.99)),
		MaxMs: round(lats[len(lats)-1]),
	}
}

func printLatencyBlock(name string, s latencyStats) {
	if s.Count == 0 {
		fmt.Printf("  %-10s 无样本\n", name)
		return
	}
	fmt.Printf("  %-10s n=%-8d avg=%-8.1f p50=%-8.1f p90=%-8.1f p95=%-8.1f p99=%-8.1f max=%.1f (ms)\n",
		name, s.Count, s.AvgMs, s.P50Ms, s.P90Ms, s.P95Ms, s.P99Ms, s.MaxMs)
}

// perfSample 网关进程/主机的一次资源采样。
type perfSample struct {
	T           string  `json:"t"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemPercent  float64 `json:"mem_percent"`
	DiskPercent float64 `json:"disk_percent"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	GoSysMB     float64 `json:"go_sys_mb"`
	Goroutines  int     `json:"goroutines"`
	NumGC       uint32  `json:"num_gc"`
}

// perfSummary 资源采样汇总。
type perfSummary struct {
	Samples        int     `json:"samples"`
	CPUAvg         float64 `json:"cpu_avg_percent"`
	CPUMax         float64 `json:"cpu_max_percent"`
	MemAvg         float64 `json:"mem_avg_percent"`
	MemMax         float64 `json:"mem_max_percent"`
	DiskAvg        float64 `json:"disk_avg_percent"`
	DiskMax        float64 `json:"disk_max_percent"`
	HeapAllocAvgMB float64 `json:"heap_alloc_avg_mb"`
	HeapAllocMaxMB float64 `json:"heap_alloc_max_mb"`
	GoroutinesAvg  int     `json:"goroutines_avg"`
	GoroutinesMax  int     `json:"goroutines_max"`
	GCCount        uint32  `json:"gc_count_delta"`
	Error          string  `json:"error,omitempty"`
}

type dbSample struct {
	T                              string `json:"t"`
	ThreadsConnected               int64  `json:"threads_connected"`
	ThreadsRunning                 int64  `json:"threads_running"`
	MaxUsedConnections             int64  `json:"max_used_connections"`
	Connections                    int64  `json:"connections"`
	AbortedConnects                int64  `json:"aborted_connects"`
	ConnectionErrorsMaxConnections int64  `json:"connection_errors_max_connections"`
	SlowQueries                    int64  `json:"slow_queries"`
	CreatedTmpDiskTables           int64  `json:"created_tmp_disk_tables"`
}

type dbSummary struct {
	Samples                             int    `json:"samples"`
	MaxConnections                      int64  `json:"max_connections"`
	ThreadsConnectedMax                 int64  `json:"threads_connected_max"`
	ThreadsRunningMax                   int64  `json:"threads_running_max"`
	MaxUsedConnectionsMax               int64  `json:"max_used_connections_max"`
	ConnectionsDelta                    int64  `json:"connections_delta"`
	AbortedConnectsDelta                int64  `json:"aborted_connects_delta"`
	ConnectionErrorsMaxConnectionsDelta int64  `json:"connection_errors_max_connections_delta"`
	SlowQueriesDelta                    int64  `json:"slow_queries_delta"`
	CreatedTmpDiskTablesDelta           int64  `json:"created_tmp_disk_tables_delta"`
	Error                               string `json:"error,omitempty"`
}

// perfStatsResponse /api/performance/stats 响应中需要的字段。
type perfStatsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		MemoryStats struct {
			Alloc        uint64 `json:"alloc"`
			Sys          uint64 `json:"sys"`
			NumGC        uint32 `json:"num_gc"`
			NumGoroutine int    `json:"num_goroutine"`
		} `json:"memory_stats"`
		SystemStatus struct {
			CPUUsage    float64 `json:"cpu_usage"`
			MemoryUsage float64 `json:"memory_usage"`
			DiskUsage   float64 `json:"disk_usage"`
		} `json:"system_status"`
	} `json:"data"`
}

func readMySQLStatus(db *sql.DB) (dbSample, error) {
	rows, err := db.Query(`SHOW GLOBAL STATUS WHERE Variable_name IN (
		'Threads_connected',
		'Threads_running',
		'Max_used_connections',
		'Connections',
		'Aborted_connects',
		'Connection_errors_max_connections',
		'Slow_queries',
		'Created_tmp_disk_tables'
	)`)
	if err != nil {
		return dbSample{}, err
	}
	defer rows.Close()
	values := map[string]int64{}
	for rows.Next() {
		var name string
		var val int64
		if err := rows.Scan(&name, &val); err != nil {
			return dbSample{}, err
		}
		values[name] = val
	}
	return dbSample{
		T:                              time.Now().Format("15:04:05"),
		ThreadsConnected:               values["Threads_connected"],
		ThreadsRunning:                 values["Threads_running"],
		MaxUsedConnections:             values["Max_used_connections"],
		Connections:                    values["Connections"],
		AbortedConnects:                values["Aborted_connects"],
		ConnectionErrorsMaxConnections: values["Connection_errors_max_connections"],
		SlowQueries:                    values["Slow_queries"],
		CreatedTmpDiskTables:           values["Created_tmp_disk_tables"],
	}, nil
}

func readMySQLMaxConnections(db *sql.DB) int64 {
	var name string
	var val int64
	if err := db.QueryRow("SHOW VARIABLES LIKE 'max_connections'").Scan(&name, &val); err != nil {
		return 0
	}
	return val
}

func collectDB(stop <-chan struct{}) ([]dbSample, int64, string) {
	db, err := sql.Open("mysql", *mysqlDSN)
	if err != nil {
		return nil, 0, err.Error()
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)
	if err := db.Ping(); err != nil {
		return nil, 0, err.Error()
	}
	maxConnections := readMySQLMaxConnections(db)
	var samples []dbSample
	var firstErr string
	sampleOnce := func() {
		s, err := readMySQLStatus(db)
		if err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			return
		}
		samples = append(samples, s)
	}
	sampleOnce()
	ticker := time.NewTicker(*dbInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sampleOnce()
		case <-stop:
			sampleOnce()
			return samples, maxConnections, firstErr
		}
	}
}

func summarizeDB(samples []dbSample, maxConnections int64, errMsg string) dbSummary {
	s := dbSummary{Samples: len(samples), MaxConnections: maxConnections, Error: errMsg}
	if len(samples) == 0 {
		return s
	}
	for _, p := range samples {
		if p.ThreadsConnected > s.ThreadsConnectedMax {
			s.ThreadsConnectedMax = p.ThreadsConnected
		}
		if p.ThreadsRunning > s.ThreadsRunningMax {
			s.ThreadsRunningMax = p.ThreadsRunning
		}
		if p.MaxUsedConnections > s.MaxUsedConnectionsMax {
			s.MaxUsedConnectionsMax = p.MaxUsedConnections
		}
	}
	first := samples[0]
	last := samples[len(samples)-1]
	s.ConnectionsDelta = last.Connections - first.Connections
	s.AbortedConnectsDelta = last.AbortedConnects - first.AbortedConnects
	s.ConnectionErrorsMaxConnectionsDelta = last.ConnectionErrorsMaxConnections - first.ConnectionErrorsMaxConnections
	s.SlowQueriesDelta = last.SlowQueries - first.SlowQueries
	s.CreatedTmpDiskTablesDelta = last.CreatedTmpDiskTables - first.CreatedTmpDiskTables
	return s
}

// collectPerf 压测期间定时采样网关资源，stop 关闭后返回采样序列。
func collectPerf(stop <-chan struct{}) ([]perfSample, string) {
	client := &http.Client{Timeout: 3 * time.Second}
	var samples []perfSample
	var firstErr string
	sampleOnce := func() {
		req, err := http.NewRequest(http.MethodGet, *perfURL, nil)
		if err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			return
		}
		if *adminToken != "" {
			req.Header.Set("Authorization", *adminToken)
		}
		if *adminUserID != "" {
			req.Header.Set("New-Api-User", *adminUserID)
		}
		resp, err := client.Do(req)
		if err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if firstErr == "" {
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
				firstErr = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(eb)))
			}
			return
		}
		var pr perfStatsResponse
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil || !pr.Success {
			if firstErr == "" {
				firstErr = "decode perf stats failed"
			}
			return
		}
		samples = append(samples, perfSample{
			T:           time.Now().Format("15:04:05"),
			CPUPercent:  pr.Data.SystemStatus.CPUUsage,
			MemPercent:  pr.Data.SystemStatus.MemoryUsage,
			DiskPercent: pr.Data.SystemStatus.DiskUsage,
			HeapAllocMB: float64(pr.Data.MemoryStats.Alloc) / 1024 / 1024,
			GoSysMB:     float64(pr.Data.MemoryStats.Sys) / 1024 / 1024,
			Goroutines:  pr.Data.MemoryStats.NumGoroutine,
			NumGC:       pr.Data.MemoryStats.NumGC,
		})
	}
	sampleOnce()
	ticker := time.NewTicker(*perfInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sampleOnce()
		case <-stop:
			sampleOnce() // 收尾再采一次
			return samples, firstErr
		}
	}
}

func summarizePerf(samples []perfSample, errMsg string) perfSummary {
	s := perfSummary{Samples: len(samples), Error: errMsg}
	if len(samples) == 0 {
		return s
	}
	var cpuSum, memSum, diskSum, heapSum float64
	var grSum int
	for _, p := range samples {
		cpuSum += p.CPUPercent
		memSum += p.MemPercent
		diskSum += p.DiskPercent
		heapSum += p.HeapAllocMB
		grSum += p.Goroutines
		if p.CPUPercent > s.CPUMax {
			s.CPUMax = p.CPUPercent
		}
		if p.MemPercent > s.MemMax {
			s.MemMax = p.MemPercent
		}
		if p.DiskPercent > s.DiskMax {
			s.DiskMax = p.DiskPercent
		}
		if p.HeapAllocMB > s.HeapAllocMaxMB {
			s.HeapAllocMaxMB = p.HeapAllocMB
		}
		if p.Goroutines > s.GoroutinesMax {
			s.GoroutinesMax = p.Goroutines
		}
	}
	n := float64(len(samples))
	round := func(v float64) float64 { return float64(int(v*10)) / 10 }
	s.CPUAvg = round(cpuSum / n)
	s.CPUMax = round(s.CPUMax)
	s.MemAvg = round(memSum / n)
	s.MemMax = round(s.MemMax)
	s.DiskAvg = round(diskSum / n)
	s.DiskMax = round(s.DiskMax)
	s.HeapAllocAvgMB = round(heapSum / n)
	s.HeapAllocMaxMB = round(s.HeapAllocMaxMB)
	s.GoroutinesAvg = grSum / len(samples)
	s.GCCount = samples[len(samples)-1].NumGC - samples[0].NumGC
	return s
}

// report 完整压测报告，-report 时序列化为 JSON。
type report struct {
	GeneratedAt string           `json:"generated_at"`
	Config      map[string]any   `json:"config"`
	ElapsedSec  float64          `json:"elapsed_sec"`
	Total       int64            `json:"total_requests"`
	Success     int64            `json:"success"`
	Failed      int64            `json:"failed"`
	Throughput  float64          `json:"throughput_rps"`
	SSEChunks   int64            `json:"sse_chunks"`
	StatusCodes map[string]int64 `json:"status_codes"`
	NonStream   latencyStats     `json:"non_stream"`
	Stream      latencyStats     `json:"stream_total"`
	StreamTTFB  latencyStats     `json:"stream_ttfb"`
	ErrorCount  int64            `json:"error_count"`
	ErrorSample []string         `json:"error_samples,omitempty"`
	PerfSummary *perfSummary     `json:"perf_summary,omitempty"`
	PerfSamples []perfSample     `json:"perf_samples,omitempty"`
	DBSummary   *dbSummary       `json:"db_summary,omitempty"`
	DBSamples   []dbSample       `json:"db_samples,omitempty"`
}

func writeChineseReport(path string, rep report) error {
	var b strings.Builder
	successRate := 0.0
	if rep.Total > 0 {
		successRate = float64(rep.Success) * 100 / float64(rep.Total)
	}
	rpm := rep.Throughput * 60
	fmt.Fprintf(&b, "# new-api 压测中文报告\n\n")
	fmt.Fprintf(&b, "## 基本信息\n\n")
	fmt.Fprintf(&b, "- 生成时间：%s\n", rep.GeneratedAt)
	fmt.Fprintf(&b, "- 压测接口：%v\n", rep.Config["url"])
	fmt.Fprintf(&b, "- 模型：%v\n", rep.Config["model"])
	fmt.Fprintf(&b, "- 并发数：%v\n", rep.Config["concurrency"])
	fmt.Fprintf(&b, "- 压测时长：%v\n", rep.Config["duration"])
	fmt.Fprintf(&b, "- 流式请求占比：%v\n", rep.Config["stream_ratio"])
	fmt.Fprintf(&b, "- max_tokens：%v\n", rep.Config["max_tokens"])
	fmt.Fprintf(&b, "- prompt 词数：%v\n\n", rep.Config["prompt_words"])

	fmt.Fprintf(&b, "## 结果汇总\n\n")
	fmt.Fprintf(&b, "- 总请求数：%d\n", rep.Total)
	fmt.Fprintf(&b, "- 成功请求数：%d\n", rep.Success)
	fmt.Fprintf(&b, "- 失败请求数：%d\n", rep.Failed)
	fmt.Fprintf(&b, "- 成功率：%.2f%%\n", successRate)
	fmt.Fprintf(&b, "- 平均吞吐：%.1f req/s\n", rep.Throughput)
	fmt.Fprintf(&b, "- 折算 RPM：约 %.0f RPM\n", rpm)
	fmt.Fprintf(&b, "- SSE chunk 数：%d\n\n", rep.SSEChunks)

	fmt.Fprintf(&b, "## 状态码分布\n\n")
	if len(rep.StatusCodes) == 0 {
		fmt.Fprintf(&b, "- 无 HTTP 状态码样本\n")
	} else {
		keys := make([]string, 0, len(rep.StatusCodes))
		for k := range rep.StatusCodes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- HTTP %s：%d\n", k, rep.StatusCodes[k])
		}
	}
	fmt.Fprintf(&b, "\n")

	writeLatency := func(title string, s latencyStats) {
		fmt.Fprintf(&b, "### %s\n\n", title)
		fmt.Fprintf(&b, "- 样本数：%d\n", s.Count)
		fmt.Fprintf(&b, "- 平均延迟：%.1f ms\n", s.AvgMs)
		fmt.Fprintf(&b, "- P50：%.1f ms\n", s.P50Ms)
		fmt.Fprintf(&b, "- P90：%.1f ms\n", s.P90Ms)
		fmt.Fprintf(&b, "- P95：%.1f ms\n", s.P95Ms)
		fmt.Fprintf(&b, "- P99：%.1f ms\n", s.P99Ms)
		fmt.Fprintf(&b, "- 最大值：%.1f ms\n\n", s.MaxMs)
	}
	fmt.Fprintf(&b, "## 延迟统计\n\n")
	writeLatency("非流式请求", rep.NonStream)
	writeLatency("流式请求总耗时", rep.Stream)
	writeLatency("流式首包 TTFB", rep.StreamTTFB)

	fmt.Fprintf(&b, "## 网关资源采样\n\n")
	if rep.PerfSummary == nil {
		fmt.Fprintf(&b, "- 未配置性能采样接口\n\n")
	} else if rep.PerfSummary.Samples == 0 {
		fmt.Fprintf(&b, "- 采样失败：%s\n\n", rep.PerfSummary.Error)
	} else {
		ps := rep.PerfSummary
		fmt.Fprintf(&b, "- 采样数：%d\n", ps.Samples)
		fmt.Fprintf(&b, "- CPU 平均：%.1f%%\n", ps.CPUAvg)
		fmt.Fprintf(&b, "- CPU 峰值：%.1f%%\n", ps.CPUMax)
		fmt.Fprintf(&b, "- 内存平均：%.1f%%\n", ps.MemAvg)
		fmt.Fprintf(&b, "- 内存峰值：%.1f%%\n", ps.MemMax)
		fmt.Fprintf(&b, "- 磁盘使用率平均：%.1f%%\n", ps.DiskAvg)
		fmt.Fprintf(&b, "- 磁盘使用率峰值：%.1f%%\n", ps.DiskMax)
		fmt.Fprintf(&b, "- Go 堆内存平均：%.1f MB\n", ps.HeapAllocAvgMB)
		fmt.Fprintf(&b, "- Go 堆内存峰值：%.1f MB\n", ps.HeapAllocMaxMB)
		fmt.Fprintf(&b, "- goroutine 平均：%d\n", ps.GoroutinesAvg)
		fmt.Fprintf(&b, "- goroutine 峰值：%d\n", ps.GoroutinesMax)
		fmt.Fprintf(&b, "- GC 增量：%d\n\n", ps.GCCount)
	}

	fmt.Fprintf(&b, "## 数据库性能采样\n\n")
	if rep.DBSummary == nil {
		fmt.Fprintf(&b, "- 未配置 MySQL DSN，未采集数据库性能信息\n\n")
	} else if rep.DBSummary.Samples == 0 {
		fmt.Fprintf(&b, "- 数据库采样失败：%s\n\n", rep.DBSummary.Error)
	} else {
		ds := rep.DBSummary
		fmt.Fprintf(&b, "- 采样数：%d\n", ds.Samples)
		fmt.Fprintf(&b, "- MySQL max_connections：%d\n", ds.MaxConnections)
		fmt.Fprintf(&b, "- Threads_connected 峰值：%d\n", ds.ThreadsConnectedMax)
		fmt.Fprintf(&b, "- Threads_running 峰值：%d\n", ds.ThreadsRunningMax)
		fmt.Fprintf(&b, "- Max_used_connections 峰值：%d\n", ds.MaxUsedConnectionsMax)
		fmt.Fprintf(&b, "- Connections 增量：%d\n", ds.ConnectionsDelta)
		fmt.Fprintf(&b, "- Aborted_connects 增量：%d\n", ds.AbortedConnectsDelta)
		fmt.Fprintf(&b, "- Connection_errors_max_connections 增量：%d\n", ds.ConnectionErrorsMaxConnectionsDelta)
		fmt.Fprintf(&b, "- Slow_queries 增量：%d\n", ds.SlowQueriesDelta)
		fmt.Fprintf(&b, "- Created_tmp_disk_tables 增量：%d\n\n", ds.CreatedTmpDiskTablesDelta)
		if ds.Error != "" {
			fmt.Fprintf(&b, "部分采样错误：%s\n\n", ds.Error)
		}
	}

	fmt.Fprintf(&b, "## 错误信息\n\n")
	if rep.ErrorCount == 0 {
		fmt.Fprintf(&b, "- 未记录到请求错误\n\n")
	} else {
		fmt.Fprintf(&b, "- 错误总数：%d\n", rep.ErrorCount)
		if len(rep.ErrorSample) > 0 {
			fmt.Fprintf(&b, "- 错误样例：\n\n")
			for _, e := range rep.ErrorSample {
				fmt.Fprintf(&b, "```text\n%s\n```\n\n", e)
			}
		}
	}

	fmt.Fprintf(&b, "## 初步结论\n\n")
	if rep.Failed > 0 || rep.ErrorCount > 0 {
		fmt.Fprintf(&b, "本轮压测出现失败请求，需要结合网关日志和数据库状态继续定位。若错误包含 `Too many connections`，优先检查数据库连接池、MySQL `max_connections`、结算/日志写库路径和批量更新配置。\n")
	} else {
		fmt.Fprintf(&b, "本轮压测未记录到失败请求，网关在当前压力下保持可用。\n")
	}
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(b.String())...)
	return os.WriteFile(path, data, 0o644)
}

func main() {
	flag.Parse()
	if *conc <= 0 {
		fmt.Fprintln(os.Stderr, "-c must be > 0")
		os.Exit(1)
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "[warn] -token 为空：仅适用于直压 mock 做基线校准")
	}
	initPromptPool()

	transport := &http.Transport{
		MaxIdleConns:        *conc * 2,
		MaxIdleConnsPerHost: *conc * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport, Timeout: *timeout}

	stats := make([]*workerStat, *conc)
	var done atomic.Int64
	deadline := time.Now().Add(*dur)

	fmt.Printf("loadgen: url=%s c=%d d=%v stream-ratio=%.2f model=%s\n",
		*target, *conc, *dur, *streamRatio, *model)

	// 实时进度
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		last := int64(0)
		for {
			select {
			case <-ticker.C:
				cur := done.Load()
				fmt.Printf("  ... 完成 %d 请求（瞬时 %.1f req/s）\n", cur, float64(cur-last)/5)
				last = cur
			case <-stopProgress:
				return
			}
		}
	}()

	// 网关资源采样
	var perfSamples []perfSample
	var perfErr string
	perfDone := make(chan struct{})
	stopPerf := make(chan struct{})
	if *perfURL != "" {
		go func() {
			perfSamples, perfErr = collectPerf(stopPerf)
			close(perfDone)
		}()
	} else {
		close(perfDone)
	}

	var dbSamples []dbSample
	var dbMaxConnections int64
	var dbErr string
	dbDone := make(chan struct{})
	stopDB := make(chan struct{})
	if *mysqlDSN != "" {
		go func() {
			dbSamples, dbMaxConnections, dbErr = collectDB(stopDB)
			close(dbDone)
		}()
	} else {
		close(dbDone)
	}

	startAll := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		st := &workerStat{codes: make(map[int]int64)}
		stats[i] = st
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(client, deadline, st, &done)
		}()
	}
	wg.Wait()
	close(stopProgress)
	close(stopPerf)
	close(stopDB)
	<-perfDone
	<-dbDone
	elapsed := time.Since(startAll)

	// 汇总
	var streamLats, streamTtfbs, plainLats []float64
	codes := make(map[int]int64)
	var errN, chunks int64
	var errSamples []string
	for _, st := range stats {
		for _, s := range st.samples {
			if s.stream {
				streamLats = append(streamLats, s.lat)
				streamTtfbs = append(streamTtfbs, s.ttfb)
			} else {
				plainLats = append(plainLats, s.lat)
			}
		}
		for c, n := range st.codes {
			codes[c] += n
		}
		errN += st.errN
		chunks += st.chunks
		for _, e := range st.errs {
			if len(errSamples) < 5 {
				errSamples = append(errSamples, e)
			}
		}
	}
	total := done.Load()
	ok := int64(len(streamLats) + len(plainLats))

	plainStats := summarize(plainLats)
	streamStats := summarize(streamLats)
	ttfbStats := summarize(streamTtfbs)

	fmt.Printf("\n===== 压测结果 =====\n")
	fmt.Printf("耗时 %.1fs  总请求 %d  成功 %d  失败 %d  吞吐 %.1f req/s  SSE chunks %d\n",
		elapsed.Seconds(), total, ok, total-ok, float64(total)/elapsed.Seconds(), chunks)
	fmt.Println("状态码分布:")
	for c, n := range codes {
		fmt.Printf("  %d: %d\n", c, n)
	}
	fmt.Println("延迟统计:")
	printLatencyBlock("非流式", plainStats)
	printLatencyBlock("流式总时长", streamStats)
	printLatencyBlock("流式TTFB", ttfbStats)
	if len(errSamples) > 0 {
		fmt.Printf("错误采样（共 %d 个错误）:\n", errN)
		for _, e := range errSamples {
			fmt.Println("  -", e)
		}
	}

	var perfSum *perfSummary
	if *perfURL != "" {
		ps := summarizePerf(perfSamples, perfErr)
		perfSum = &ps
		fmt.Println("网关资源（压测期间采样）:")
		if ps.Samples == 0 {
			fmt.Printf("  采样失败: %s\n", ps.Error)
		} else {
			fmt.Printf("  CPU avg=%.1f%% max=%.1f%%  内存 avg=%.1f%% max=%.1f%%\n",
				ps.CPUAvg, ps.CPUMax, ps.MemAvg, ps.MemMax)
			fmt.Printf("  Go堆 avg=%.1fMB max=%.1fMB  goroutines avg=%d max=%d  GC次数 +%d  样本数 %d\n",
				ps.HeapAllocAvgMB, ps.HeapAllocMaxMB, ps.GoroutinesAvg, ps.GoroutinesMax, ps.GCCount, ps.Samples)
			if ps.Error != "" {
				fmt.Printf("  （部分采样失败: %s）\n", ps.Error)
			}
		}
	}

	var dbSum *dbSummary
	if *mysqlDSN != "" {
		ds := summarizeDB(dbSamples, dbMaxConnections, dbErr)
		dbSum = &ds
		fmt.Println("数据库资源（压测期间采样）:")
		if ds.Samples == 0 {
			fmt.Printf("  采样失败: %s\n", ds.Error)
		} else {
			fmt.Printf("  max_connections=%d  Threads_connected max=%d  Threads_running max=%d  Max_used_connections max=%d\n",
				ds.MaxConnections, ds.ThreadsConnectedMax, ds.ThreadsRunningMax, ds.MaxUsedConnectionsMax)
			fmt.Printf("  Connections +%d  Aborted_connects +%d  MaxConnErrors +%d  SlowQueries +%d  TmpDiskTables +%d  样本数 %d\n",
				ds.ConnectionsDelta, ds.AbortedConnectsDelta, ds.ConnectionErrorsMaxConnectionsDelta, ds.SlowQueriesDelta, ds.CreatedTmpDiskTablesDelta, ds.Samples)
			if ds.Error != "" {
				fmt.Printf("  （部分采样失败: %s）\n", ds.Error)
			}
		}
	}

	if *reportPath != "" || *reportZhPath != "" {
		codeMap := make(map[string]int64, len(codes))
		for c, n := range codes {
			codeMap[fmt.Sprintf("%d", c)] = n
		}
		rep := report{
			GeneratedAt: time.Now().Format(time.RFC3339),
			Config: map[string]any{
				"url": *target, "model": *model, "concurrency": *conc,
				"duration": dur.String(), "stream_ratio": *streamRatio,
				"max_tokens": *maxTokens, "prompt_words": *promptWords,
			},
			ElapsedSec:  float64(int(elapsed.Seconds()*10)) / 10,
			Total:       total,
			Success:     ok,
			Failed:      total - ok,
			Throughput:  float64(int(float64(total)/elapsed.Seconds()*10)) / 10,
			SSEChunks:   chunks,
			StatusCodes: codeMap,
			NonStream:   plainStats,
			Stream:      streamStats,
			StreamTTFB:  ttfbStats,
			ErrorCount:  errN,
			ErrorSample: errSamples,
			PerfSummary: perfSum,
			PerfSamples: perfSamples,
			DBSummary:   dbSum,
			DBSamples:   dbSamples,
		}
		if *reportZhPath != "" {
			if err := writeChineseReport(*reportZhPath, rep); err != nil {
				fmt.Fprintln(os.Stderr, "write zh report:", err)
				return
			}
			fmt.Printf("中文报告已写入 %s\n", *reportZhPath)
		}
		if *reportPath == "" {
			return
		}
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal report:", err)
			return
		}
		if err := os.WriteFile(*reportPath, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write report:", err)
			return
		}
		fmt.Printf("报告已写入 %s\n", *reportPath)
	}
}
