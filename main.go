package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/pprof_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

//go:embed web/dist
var buildFS embed.FS

//go:embed web/dist/index.html
var indexPage []byte

func main() {
	startTime := time.Now()
	kitutil.SetLogging(common.SysLog, func(message string) {
		logger.LogError(nil, message)
	})
	kitutil.SetSystemErrorLogging(common.SysError)

	err := InitResources()
	if err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}

	common.SysLog("NEXAXIS API " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	kitutil.Debug.Store(common.DebugEnabled)

	defer func() {
		err := model.CloseDB()
		if err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}()

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						common.FatalLog(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			model.InitChannelCache()
		}()

		go model.SyncChannelCache(common.SyncFrequency)
	}

	// Warm pricing after channel cache initialization so Advanced Custom
	// endpoint inference can read cached route settings on first request.
	model.GetPricing()

	// 热更新配置
	go model.SyncOptions(common.SyncFrequency)

	// 周期性重载授权策略，保证多节点/多 master 部署下权限变更能传播到每个实例
	go authz.StartPolicySync(common.SyncFrequency)

	// 数据看板
	go model.UpdateQuotaData()

	// 业务概览日统计缓冲刷盘（成本/提成/配额，使用 BUSINESS_STATS_FLUSH_INTERVAL 配置）
	model.StartBusinessStatsFlushLoop()
	model.StartRelayLogFlushLoop()

	// 使用日志列表/统计的整点预热（master 独占，每小时约 1 条查询）。
	// 让管理后台的默认视图直接命中缓存，而不是每次开页都对 logs 做全区间扫描。
	model.StartLogStatWarmLoop()

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			common.FatalLog("failed to parse CHANNEL_UPDATE_FREQUENCY: " + err.Error())
		}
		go controller.AutomaticallyUpdateChannels(frequency)
	}

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day
	service.StartCodexCredentialAutoRefreshTask()

	// Subscription quota reset task (daily/weekly/monthly/custom)
	service.StartSubscriptionQuotaResetTask()

	// Periodic read-only comparison of marketplace pricing sources.
	controller.StartPriceMonitorTask()

	// Commission tier / performance period monthly auto-reset task
	service.StartCommissionTierResetTask()

	// Report this process as a system instance so the System Info page can show
	// all currently alive nodes in multi-instance deployments.
	service.StartSystemInstanceReporter()

	// Wire task polling adaptor factory (breaks service -> relay import cycle).
	// Must run before the system task runner starts: the async_task_poll handler
	// calls service.RunTaskPollingOnce, which needs this factory set.
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Register the periodic channel test, upstream model update, and async task
	// polling (Midjourney / Suno / video) jobs as scheduled system tasks
	// (DB-lease dedup across masters + run history), then start the runner that
	// schedules and executes them. Master-only execution and the UpdateTask
	// switch are enforced inside the runner and each handler's Enabled().
	controller.RegisterScheduledSystemTasks()
	service.StartSystemTaskRunner()

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		model.InitBatchUpdater()
	}

	// pprof 下载开关（pprof_setting.enabled）由配置系统热更新，每个请求现读现判，
	// 无需在这里启动任何后台 goroutine。ENABLE_PPROF 仅作为启动默认值，已在
	// InitOptionMap 之前经 pprof_setting.ApplyEnvDefaults() 应用。

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	if err := middleware.ConfigureTrustedProxies(server); err != nil {
		common.FatalLog("failed to configure trusted proxies: " + err.Error())
		return
	}
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		// 堆栈是必须的：这是全站唯一生效的 panic 兜底，relay 在内的所有路由都靠它。
		// 只打一行 "panic detected" 无法定位是哪个 adaptor / handler 出的问题。
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		common.SysError(fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Version())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 设置路由
	router.SetRouter(server, router.WebAssets{
		BuildFS:   buildFS,
		IndexPage: indexPage,
	})
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: server,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.FatalLog("failed to start HTTP server: " + err.Error())
		}
	}()

	time.Sleep(100 * time.Millisecond)

	common.LogStartupSuccess(startTime, port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	// SSE streams may run for minutes; give them time to finish before forced exit
	shutdownTimeout := time.Duration(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", 120)) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownSequence(
		func() error {
			common.SysLog("received shutdown signal, draining in-flight requests...")
			return srv.Shutdown(shutdownCtx)
		},
		func() {
			// Stop flush loop, flush to DB, drain remainder to fallback file.
			// Budget 来自 ledger_pipeline_setting.shutdown_timeout_sec（默认 25 s）。
			// systemd 的 TimeoutStopSec 必须 >= SHUTDOWN_TIMEOUT_SECONDS 加上这个预算，
			// 否则 SIGKILL 会落在刷盘之前。
			common.SysLog("requests drained, flushing business stat buffers...")
			model.ShutdownStatsFlush(operation_setting.GetLedgerPipelineSetting().GetShutdownTimeout())
			model.ShutdownRelayLogFlush(operation_setting.GetRelayLogPipelineSetting().GetShutdownTimeout())

			// 请求日志：先排空写队列让在途条目落盘并进索引，快照才是完整的。
			// 预算 3 s——这些数据允许丢失，不值得再拖长关停。
			middleware.DrainRequestLogQueue(3 * time.Second)
			model.SnapshotRequestLogs()
		},
	)

	// 内存中的看板数据保存入库，避免重启丢失未落库数据 (issue #5679)
	if common.DataExportEnabled {
		model.SaveQuotaDataCache()
	}
	common.SysLog("server exited")
}

// shutdownSequence 执行关停：先排空 HTTP 在途请求（drain），再刷台账缓冲（flush）。
//
// 顺序不可颠倒。在途请求是在 drain 期间陆续结束的，每个结束的请求才把成本/提成记录
// 写进内存缓冲；而 flush 会把刷盘 goroutine 永久关掉。若 flush 排在前面，排空窗口
// （SHUTDOWN_TIMEOUT_SECONDS，默认 120 s）内完成的请求写入缓冲后再无人落库，
// 进程退出即静默丢失 —— 不报错，也不会落兜底文件，因为兜底文件是 flush 自己执行
// 期间写的，那时这些记录还没产生。
//
// 按现在的顺序，drain 期间刷盘 goroutine 仍按 FlushIntervalSec 正常工作，即使进程
// 被 SIGKILL 打断，暴露面也只有最后一个刷盘间隔，而不是整个排空窗口。
//
// 抽成独立函数是为了让 TestShutdownSequence_DrainsBeforeFlush 能锁住这个顺序。
func shutdownSequence(drain func() error, flush func()) {
	if err := drain(); err != nil {
		common.SysError("server shutdown error: " + err.Error())
	}
	flush()
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	common.InitEnv()

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	service.InitTokenEncoders()

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}
	if err = authz.Init(model.DB); err != nil {
		common.FatalLog("failed to initialize authorization: " + err.Error())
		return err
	}

	model.CheckSetup()

	// ENABLE_PPROF=true 只作为 pprof 配置的启动默认值，必须在 InitOptionMap 之前应用：
	// InitOptionMap 会先导出内置默认值，再用数据库里的值覆盖（DB > env > 内置默认）。
	pprof_setting.ApplyEnvDefaults()
	operation_setting.ApplyRateLimitEnvDefaults()
	operation_setting.ApplyDBPoolEnvDefaults()
	operation_setting.ApplyUserSessionEnvDefaults()
	operation_setting.ApplyRelayTimeoutEnvDefaults()

	// Initialize options, should after model.InitDB()
	if common.IsMasterNode {
		if err := model.MigrateRetiredFrontendOptions(); err != nil {
			common.SysError("failed to migrate retired frontend options: " + err.Error())
		}
	}
	model.InitOptionMap()
	operation_setting.PublishRateLimitSetting()
	operation_setting.PublishDBPoolSetting()
	operation_setting.PublishUserSessionSetting()
	if err := model.ApplyDBPoolSetting(); err != nil {
		common.SysError("failed to apply database pool settings: " + err.Error())
	}

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// Initialize SQL Database
	logDBStartedAt := time.Now()
	common.SysLog("log database initialization started")
	err = model.InitLogDB()
	if err != nil {
		return err
	}
	if err := model.ApplyDBPoolSetting(); err != nil {
		common.SysError("failed to apply log database pool settings: " + err.Error())
	}
	common.SysLog(fmt.Sprintf("log database initialization completed in %s", time.Since(logDBStartedAt)))

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	// 请求日志：环境变量必须在 godotenv.Load 之后读，因此这里显式初始化而不是靠包级变量
	// （包级变量在 main() 之前求值，那时 .env 还没进环境，配置会被静默忽略）。
	// 三步顺序不可颠倒：先恢复索引快照，再拉起写盘 worker 与磁盘清理——清理以内存索引
	// 为唯一真值，若排在恢复之前，首轮就会把有效正文文件当孤儿删掉。
	model.InitRequestLogStore()
	model.RestoreRequestLogs()
	middleware.StartRequestLogWriters()
	model.StartRequestLogSweeper()
	model.StartLegacyRequestLogCleanup()

	perfmetrics.Init()

	// 启动系统监控
	common.StartSystemMonitor()

	// 上次进程遗留的导出任务没有 goroutine 继续推进，置为失败并清理半成品文件。
	go model.RecoverStaleLogExportJobs()

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
		// Don't return error, i18n is not critical
	} else {
		common.SysLog("i18n initialized with languages: " + strings.Join(i18n.SupportedLanguages(), ", "))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
		// Don't return error, custom OAuth is not critical
	}

	service.StartAuthArtifactCleanup()

	return nil
}
