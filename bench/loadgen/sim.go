package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// sim.go 提供「生产环境模拟」模式（-sim）：并发随昼夜曲线 + 抖动 + 随机突发变化，每个
// 虚拟用户请求之间带思考间隔，多种请求类型按权重混合，适合长时间运行。统计用固定内存的
// 对数直方图（长压不 OOM），并按 -report-interval 滚动打印区间 RPS / p50 / p95 / p99。

var (
	simMode      = flag.Bool("sim", false, "生产环境模拟模式：并发随昼夜曲线+抖动+突发变化，用户带思考间隔，多类型请求混合，适合长跑")
	simPeriod    = flag.Duration("sim-period", 10*time.Minute, "一个完整昼夜周期时长（并发从低谷→高峰→回落）")
	simDayFrac   = flag.Float64("sim-day-frac", 0.6, "白天高峰并发占 -c 的比例 [0,1]")
	simNightFrac = flag.Float64("sim-night-frac", 0.1, "夜间低谷并发占 -c 的比例 [0,1]")
	simJitter    = flag.Float64("sim-jitter", 0.15, "并发随机抖动幅度 [0,1)")
	simSpikeRate = flag.Float64("sim-spike-rate", 0.03, "每个调节 tick 触发突发流量的概率 [0,1]")
	simSpikeMult = flag.Float64("sim-spike-mult", 2.5, "突发时并发放大倍数（上限仍为 -c）")
	simThinkMin  = flag.Duration("sim-think-min", 500*time.Millisecond, "用户两次请求之间的思考间隔下限")
	simThinkMax  = flag.Duration("sim-think-max", 8*time.Second, "用户思考间隔上限")
	simMix       = flag.String("sim-mix", "chat-stream:45,chat:35,image:10,speech:7,transcription:3", "请求类型权重 kind:weight,...（kind: chat|chat-stream|claude|claude-stream|image|speech|transcription）")
	simTick      = flag.Duration("sim-tick", 3*time.Second, "并发调节 tick")
	reportEvery  = flag.Duration("report-interval", 30*time.Second, "滚动报告间隔")
	imageModel   = flag.String("image-model", "dall-e-3", "sim 混合中 image 请求用的模型")
	ttsModel     = flag.String("tts-model", "tts-1", "sim 混合中 speech 请求用的模型")
	sttModel     = flag.String("stt-model", "whisper-1", "sim 混合中 transcription 请求用的模型")
	claudeModel  = flag.String("claude-model", "claude-3-5-sonnet-20241022", "sim 混合中 claude 请求用的模型")
)

// ---- 对数直方图（固定内存，适合长跑）----

const (
	histBuckets = 4096
	histMinMs   = 0.1
	histMaxMs   = 600000 // 10min
)

var histLogRange = math.Log(histMaxMs / histMinMs)

type histogram struct {
	buckets [histBuckets]atomic.Int64
	count   atomic.Int64
	sumMics atomic.Int64 // 累计微秒，用于均值
}

func histBucketIndex(ms float64) int {
	if ms <= histMinMs {
		return 0
	}
	if ms >= histMaxMs {
		return histBuckets - 1
	}
	return int(float64(histBuckets) * math.Log(ms/histMinMs) / histLogRange)
}

func histBucketValue(b int) float64 {
	return histMinMs * math.Exp(histLogRange*(float64(b)+0.5)/float64(histBuckets))
}

func (h *histogram) Add(ms float64) {
	h.count.Add(1)
	h.sumMics.Add(int64(ms * 1000))
	h.buckets[histBucketIndex(ms)].Add(1)
}

func (h *histogram) snapshot() ([histBuckets]int64, int64) {
	var b [histBuckets]int64
	var total int64
	for i := range h.buckets {
		v := h.buckets[i].Load()
		b[i] = v
		total += v
	}
	return b, total
}

// percentileFrom 从 bucket 计数切片求分位（毫秒）。
func percentileFrom(b *[histBuckets]int64, total int64, p float64) float64 {
	if total == 0 {
		return 0
	}
	target := int64(float64(total) * p)
	var cum int64
	for i := 0; i < histBuckets; i++ {
		cum += b[i]
		if cum >= target {
			return histBucketValue(i)
		}
	}
	return histBucketValue(histBuckets - 1)
}

func maxFrom(b *[histBuckets]int64) float64 {
	for i := histBuckets - 1; i >= 0; i-- {
		if b[i] > 0 {
			return histBucketValue(i)
		}
	}
	return 0
}

// ---- 请求类型 ----

type simKind struct {
	name   string
	format string
	url    string
	model  string
	stream bool
}

// simBase 把 -url 归一为网关根地址（去掉常见 endpoint 后缀），供各类型拼接自己的 endpoint。
func simBase() string {
	u := strings.TrimRight(*target, "/")
	for _, suf := range []string{
		"/v1/chat/completions", "/v1/messages", "/v1/images/generations",
		"/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/translations",
	} {
		u = strings.TrimSuffix(u, suf)
	}
	return strings.TrimRight(u, "/")
}

func kindByName(name, base string) (simKind, bool) {
	switch name {
	case "chat":
		return simKind{name, "openai", base + "/v1/chat/completions", *model, false}, true
	case "chat-stream":
		return simKind{name, "openai", base + "/v1/chat/completions", *model, true}, true
	case "claude":
		return simKind{name, "claude", base + "/v1/messages", *claudeModel, false}, true
	case "claude-stream":
		return simKind{name, "claude", base + "/v1/messages", *claudeModel, true}, true
	case "image":
		return simKind{name, "image", base + "/v1/images/generations", *imageModel, false}, true
	case "speech":
		return simKind{name, "speech", base + "/v1/audio/speech", *ttsModel, false}, true
	case "transcription":
		return simKind{name, "transcription", base + "/v1/audio/transcriptions", *sttModel, false}, true
	}
	return simKind{}, false
}

// buildSimKinds 解析 -sim-mix，返回类型表与按权重展开的采样池。
func buildSimKinds(base string) ([]simKind, []int, error) {
	var kinds []simKind
	var pool []int
	for _, part := range strings.Split(*simMix, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, ws, ok := strings.Cut(part, ":")
		if !ok {
			ws = "1"
		}
		name = strings.TrimSpace(name)
		k, valid := kindByName(name, base)
		if !valid {
			return nil, nil, fmt.Errorf("unknown sim kind %q", name)
		}
		weight, err := strconv.Atoi(strings.TrimSpace(ws))
		if err != nil || weight < 1 {
			return nil, nil, fmt.Errorf("invalid weight for %q", name)
		}
		idx := len(kinds)
		kinds = append(kinds, k)
		for i := 0; i < weight; i++ {
			pool = append(pool, idx)
		}
	}
	if len(kinds) == 0 {
		return nil, nil, fmt.Errorf("empty -sim-mix")
	}
	return kinds, pool, nil
}

// ---- 统计 ----

var trackedCodes = []int{200, 400, 401, 403, 404, 408, 429, 500, 502, 503, 504}

func codeSlot(c int) int {
	for i, v := range trackedCodes {
		if v == c {
			return i
		}
	}
	return len(trackedCodes) // 最后一格 = other
}

type simStats struct {
	hist       histogram
	total      atomic.Int64
	ok         atomic.Int64
	errN       atomic.Int64
	codeCounts []atomic.Int64 // len = len(trackedCodes)+1（末位 = other）
	kindCounts []atomic.Int64
	mu         sync.Mutex
	errSamples []string
}

func newSimStats(nKinds int) *simStats {
	return &simStats{
		codeCounts: make([]atomic.Int64, len(trackedCodes)+1),
		kindCounts: make([]atomic.Int64, nKinds),
	}
}

func (st *simStats) recordErr(msg string) {
	st.mu.Lock()
	if len(st.errSamples) < 10 {
		st.errSamples = append(st.errSamples, msg)
	}
	st.mu.Unlock()
}

func msSince(start time.Time) float64 { return float64(time.Since(start)) / 1e6 }

func drainResponse(resp *http.Response, isStream bool, start time.Time) float64 {
	var ttfb float64
	if isStream {
		br := bufio.NewReaderSize(resp.Body, 16<<10)
		first := true
		for {
			line, rerr := br.ReadString('\n')
			if first && line != "" {
				ttfb = msSince(start)
				first = false
			}
			if strings.Contains(line, "[DONE]") || rerr != nil {
				break
			}
		}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return ttfb
}

// ---- 虚拟用户 ----

func simWorker(idx int, client *http.Client, deadline time.Time, activeTarget *atomic.Int64, kinds []simKind, pool []int, st *simStats) {
	buf := new(bytes.Buffer)
	for time.Now().Before(deadline) {
		// 只有序号在当前活跃并发内的用户才发压，其余空闲轮询。
		if int64(idx) >= activeTarget.Load() {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		kindIdx := pool[rand.IntN(len(pool))]
		k := kinds[kindIdx]
		buildBody(buf, k.format, k.model, k.stream)
		req, err := http.NewRequest(http.MethodPost, k.url, bytes.NewReader(buf.Bytes()))
		if err != nil {
			st.recordErr("build request: " + err.Error())
			return
		}
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		req.Header.Set("Content-Type", contentType(k.format))

		start := time.Now()
		resp, err := client.Do(req)
		st.total.Add(1)
		st.kindCounts[kindIdx].Add(1)
		if err != nil {
			st.errN.Add(1)
			st.codeCounts[len(trackedCodes)].Add(1)
			st.recordErr(err.Error())
			simThink()
			continue
		}
		st.codeCounts[codeSlot(resp.StatusCode)].Add(1)
		if resp.StatusCode == http.StatusOK {
			_ = drainResponse(resp, k.stream, start)
			st.ok.Add(1)
			st.hist.Add(msSince(start))
		} else {
			eb, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			st.errN.Add(1)
			st.recordErr(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(eb))))
		}
		_ = resp.Body.Close()
		simThink()
	}
}

func simThink() {
	if *simThinkMax <= *simThinkMin {
		time.Sleep(*simThinkMin)
		return
	}
	time.Sleep(*simThinkMin + time.Duration(rand.Int64N(int64(*simThinkMax-*simThinkMin))))
}

// ---- 并发调节（昼夜曲线 + 抖动 + 突发）----

func simController(start, deadline time.Time, activeTarget *atomic.Int64, peak int, stop <-chan struct{}) {
	ticker := time.NewTicker(*simTick)
	defer ticker.Stop()
	periodSec := simPeriod.Seconds()
	var spikeUntil time.Time
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if !now.Before(deadline) {
				return
			}
			el := now.Sub(start).Seconds()
			// 昼夜形状：0 起点低谷 → 0.5 高峰 → 1 回落
			phase := math.Mod(el, periodSec) / periodSec
			shape := 0.5 - 0.5*math.Cos(2*math.Pi*phase)
			frac := *simNightFrac + (*simDayFrac-*simNightFrac)*shape
			target := frac * float64(peak)
			// 抖动
			target *= 1 + *simJitter*(rand.Float64()*2-1)
			// 突发
			if now.Before(spikeUntil) {
				target *= *simSpikeMult
			} else if rand.Float64() < *simSpikeRate {
				spikeUntil = now.Add(10*time.Second + time.Duration(rand.Int64N(int64(50*time.Second))))
			}
			ti := int64(math.Round(target))
			if ti < 1 {
				ti = 1
			}
			if ti > int64(peak) {
				ti = int64(peak)
			}
			activeTarget.Store(ti)
		}
	}
}

// ---- 滚动报告 ----

type simSnapshot struct {
	T            string  `json:"t"`
	ElapsedSec   float64 `json:"elapsed_sec"`
	ActiveUsers  int64   `json:"active_users"`
	IntervalReqs int64   `json:"interval_reqs"`
	IntervalRPS  float64 `json:"interval_rps"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	P99Ms        float64 `json:"p99_ms"`
	CumReqs      int64   `json:"cum_reqs"`
	CumErrs      int64   `json:"cum_errs"`
}

func round1(v float64) float64 { return float64(int(v*10)) / 10 }

func simReporter(start, deadline time.Time, activeTarget *atomic.Int64, st *simStats, stop <-chan struct{}) []simSnapshot {
	ticker := time.NewTicker(*reportEvery)
	defer ticker.Stop()
	var series []simSnapshot
	prev, prevTotal := st.hist.snapshot()
	prevAll := st.total.Load()
	last := start
	emit := func(now time.Time) {
		cur, curTotal := st.hist.snapshot()
		var delta [histBuckets]int64
		for i := range cur {
			delta[i] = cur[i] - prev[i]
		}
		dCount := curTotal - prevTotal
		curAll := st.total.Load()
		dAll := curAll - prevAll
		secs := now.Sub(last).Seconds()
		if secs <= 0 {
			secs = 1
		}
		snap := simSnapshot{
			T:            now.Format("15:04:05"),
			ElapsedSec:   round1(now.Sub(start).Seconds()),
			ActiveUsers:  activeTarget.Load(),
			IntervalReqs: dAll,
			IntervalRPS:  round1(float64(dAll) / secs),
			P50Ms:        round1(percentileFrom(&delta, dCount, 0.50)),
			P95Ms:        round1(percentileFrom(&delta, dCount, 0.95)),
			P99Ms:        round1(percentileFrom(&delta, dCount, 0.99)),
			CumReqs:      curAll,
			CumErrs:      st.errN.Load(),
		}
		series = append(series, snap)
		fmt.Printf("  [%s +%.0fs] 并发=%d  区间 %d 请求 %.1f req/s  p50=%.0f p95=%.0f p99=%.0f ms  累计 %d（错误 %d）\n",
			snap.T, snap.ElapsedSec, snap.ActiveUsers, snap.IntervalReqs, snap.IntervalRPS, snap.P50Ms, snap.P95Ms, snap.P99Ms, snap.CumReqs, snap.CumErrs)
		prev, prevTotal, prevAll, last = cur, curTotal, curAll, now
	}
	for {
		select {
		case <-stop:
			emit(time.Now())
			return series
		case now := <-ticker.C:
			if !now.Before(deadline) {
				emit(now)
				return series
			}
			emit(now)
		}
	}
}

// ---- 编排 ----

func runSim() {
	base := simBase()
	kinds, pool, err := buildSimKinds(base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	peak := *conc
	if peak < 1 {
		peak = 1
	}

	transport := &http.Transport{
		MaxIdleConns:        peak * 2,
		MaxIdleConnsPerHost: peak * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport, Timeout: *timeout}
	st := newSimStats(len(kinds))

	var activeTarget atomic.Int64
	activeTarget.Store(int64(math.Max(1, float64(peak)**simNightFrac)))
	start := time.Now()
	deadline := start.Add(*dur)

	mixDesc := make([]string, len(kinds))
	for i, k := range kinds {
		mixDesc[i] = k.name
	}
	fmt.Printf("loadgen[sim]: base=%s peak-c=%d d=%v period=%v day/night=%.2f/%.2f think=[%v,%v] mix=%s\n",
		base, peak, *dur, *simPeriod, *simDayFrac, *simNightFrac, *simThinkMin, *simThinkMax, strings.Join(mixDesc, "+"))

	// 资源采样（复用闭环模式的采集器）
	var perfSamples []perfSample
	var perfErr string
	perfDone := make(chan struct{})
	stopPerf := make(chan struct{})
	if *perfURL != "" {
		go func() { perfSamples, perfErr = collectPerf(stopPerf); close(perfDone) }()
	} else {
		close(perfDone)
	}
	var dbSamples []dbSample
	var dbMaxConn int64
	var dbErr string
	dbDone := make(chan struct{})
	stopDB := make(chan struct{})
	if *mysqlDSN != "" {
		go func() { dbSamples, dbMaxConn, dbErr = collectDB(stopDB); close(dbDone) }()
	} else {
		close(dbDone)
	}

	stopCtl := make(chan struct{})
	go simController(start, deadline, &activeTarget, peak, stopCtl)
	stopRep := make(chan struct{})
	repDone := make(chan struct{})
	var series []simSnapshot
	go func() { series = simReporter(start, deadline, &activeTarget, st, stopRep); close(repDone) }()

	var wg sync.WaitGroup
	for i := 0; i < peak; i++ {
		wg.Add(1)
		go func(idx int) { defer wg.Done(); simWorker(idx, client, deadline, &activeTarget, kinds, pool, st) }(i)
	}
	wg.Wait()
	close(stopCtl)
	close(stopRep)
	<-repDone
	close(stopPerf)
	close(stopDB)
	<-perfDone
	<-dbDone

	simSummary(start, kinds, st, series, perfSamples, perfErr, dbSamples, dbMaxConn, dbErr)
}

// simReport 是 -report 时序列化的 sim 报告。
type simReport struct {
	GeneratedAt string           `json:"generated_at"`
	Mode        string           `json:"mode"`
	Config      map[string]any   `json:"config"`
	ElapsedSec  float64          `json:"elapsed_sec"`
	Total       int64            `json:"total_requests"`
	Success     int64            `json:"success"`
	Failed      int64            `json:"failed"`
	AvgRPS      float64          `json:"avg_rps"`
	Latency     latencyStats     `json:"latency"`
	StatusCodes map[string]int64 `json:"status_codes"`
	KindCounts  map[string]int64 `json:"kind_counts"`
	Timeline    []simSnapshot    `json:"timeline"`
	ErrorSample []string         `json:"error_samples,omitempty"`
	PerfSummary *perfSummary     `json:"perf_summary,omitempty"`
	PerfSamples []perfSample     `json:"perf_samples,omitempty"`
	DBSummary   *dbSummary       `json:"db_summary,omitempty"`
	DBSamples   []dbSample       `json:"db_samples,omitempty"`
}

func simSummary(start time.Time, kinds []simKind, st *simStats, series []simSnapshot,
	perfSamples []perfSample, perfErr string, dbSamples []dbSample, dbMaxConn int64, dbErr string) {

	elapsed := time.Since(start)
	buckets, total := st.hist.snapshot()
	okN := st.ok.Load()
	totalN := st.total.Load()
	errN := st.errN.Load()

	avgMs := 0.0
	if total > 0 {
		avgMs = round1(float64(st.hist.sumMics.Load()) / float64(total) / 1000)
	}
	lat := latencyStats{
		Count: int(okN),
		AvgMs: avgMs,
		P50Ms: round1(percentileFrom(&buckets, total, 0.50)),
		P90Ms: round1(percentileFrom(&buckets, total, 0.90)),
		P95Ms: round1(percentileFrom(&buckets, total, 0.95)),
		P99Ms: round1(percentileFrom(&buckets, total, 0.99)),
		MaxMs: round1(maxFrom(&buckets)),
	}

	codes := map[string]int64{}
	for i, c := range trackedCodes {
		if n := st.codeCounts[i].Load(); n > 0 {
			codes[strconv.Itoa(c)] = n
		}
	}
	if n := st.codeCounts[len(trackedCodes)].Load(); n > 0 {
		codes["other/网络错误"] = n
	}
	kindCounts := map[string]int64{}
	for i, k := range kinds {
		kindCounts[k.name] = st.kindCounts[i].Load()
	}

	fmt.Printf("\n===== 生产模拟结果 =====\n")
	fmt.Printf("运行 %.0fs  总请求 %d  成功 %d  失败 %d  平均吞吐 %.1f req/s\n",
		elapsed.Seconds(), totalN, okN, errN, float64(totalN)/elapsed.Seconds())
	fmt.Printf("延迟(全程聚合)  avg=%.0f p50=%.0f p90=%.0f p95=%.0f p99=%.0f max=%.0f (ms)\n",
		lat.AvgMs, lat.P50Ms, lat.P90Ms, lat.P95Ms, lat.P99Ms, lat.MaxMs)

	fmt.Println("按类型请求数:")
	for _, k := range kinds {
		fmt.Printf("  %-14s %d\n", k.name, kindCounts[k.name])
	}
	fmt.Println("状态码分布:")
	ckeys := make([]string, 0, len(codes))
	for c := range codes {
		ckeys = append(ckeys, c)
	}
	sort.Strings(ckeys)
	for _, c := range ckeys {
		fmt.Printf("  %-14s %d\n", c, codes[c])
	}
	st.mu.Lock()
	errSamples := append([]string(nil), st.errSamples...)
	st.mu.Unlock()
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
		if ps.Samples > 0 {
			fmt.Printf("网关资源: CPU avg=%.1f%% max=%.1f%%  内存 avg=%.1f%% max=%.1f%%  goroutines max=%d  GC +%d\n",
				ps.CPUAvg, ps.CPUMax, ps.MemAvg, ps.MemMax, ps.GoroutinesMax, ps.GCCount)
		}
	}
	var dbSum *dbSummary
	if *mysqlDSN != "" {
		ds := summarizeDB(dbSamples, dbMaxConn, dbErr)
		dbSum = &ds
		if ds.Samples > 0 {
			fmt.Printf("数据库: max_connections=%d Threads_connected max=%d SlowQueries +%d\n",
				ds.MaxConnections, ds.ThreadsConnectedMax, ds.SlowQueriesDelta)
		}
	}

	if *reportPath == "" {
		return
	}
	rep := simReport{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Mode:        "sim",
		Config: map[string]any{
			"url": *target, "peak_concurrency": *conc, "duration": dur.String(),
			"sim_period": simPeriod.String(), "day_frac": *simDayFrac, "night_frac": *simNightFrac,
			"jitter": *simJitter, "spike_rate": *simSpikeRate, "spike_mult": *simSpikeMult,
			"think_min": simThinkMin.String(), "think_max": simThinkMax.String(), "mix": *simMix,
		},
		ElapsedSec: round1(elapsed.Seconds()),
		Total:      totalN, Success: okN, Failed: errN,
		AvgRPS:      round1(float64(totalN) / elapsed.Seconds()),
		Latency:     lat,
		StatusCodes: codes,
		KindCounts:  kindCounts,
		Timeline:    series,
		ErrorSample: errSamples,
		PerfSummary: perfSum,
		PerfSamples: perfSamples,
		DBSummary:   dbSum,
		DBSamples:   dbSamples,
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal sim report:", err)
		return
	}
	if err := os.WriteFile(*reportPath, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write sim report:", err)
		return
	}
	fmt.Printf("报告已写入 %s\n", *reportPath)
}
