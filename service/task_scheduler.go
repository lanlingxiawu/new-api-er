package service

// task_scheduler.go — Cluster-safe periodic task runner.
//
// Each task is registered with:
//   - a name matching a row in scheduler_configs table
//   - requiresMaster=true: only the node that wins the distributed lock runs it
//   - requiresMaster=false: every node runs it (e.g. channel auto-test)
//
// The scheduler polls the DB config every 10 s. When next_run_time has passed
// and the task is enabled, it executes the task and updates next_run_time.
// The 1-minute config cache means parameter changes take effect within 1 minute.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const schedulerPollInterval = 10 * time.Second
const configCacheTTL = 60 * time.Second

// ScheduledTask is a registered task handler.
type ScheduledTask struct {
	Name           string
	RequiresMaster bool
	Handler        func(ctx context.Context) error
}

type configCache struct {
	mu        sync.RWMutex
	data      map[string]*model.SchedulerConfig
	fetchedAt time.Time
}

func (c *configCache) get(taskName string) (*model.SchedulerConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.fetchedAt) > configCacheTTL {
		return nil, false
	}
	cfg, ok := c.data[taskName]
	return cfg, ok
}

func (c *configCache) refresh() error {
	cfgs, err := model.GetAllSchedulerConfigs()
	if err != nil {
		return err
	}
	m := make(map[string]*model.SchedulerConfig, len(cfgs))
	for i := range cfgs {
		m[cfgs[i].TaskName] = &cfgs[i]
	}
	c.mu.Lock()
	c.data = m
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return nil
}

// TaskScheduler coordinates all periodic tasks in a cluster-safe way.
type TaskScheduler struct {
	mu       sync.RWMutex
	tasks    map[string]*ScheduledTask
	cache    *configCache
	stopChan chan struct{}
	running  bool
}

var globalScheduler = &TaskScheduler{
	tasks:    make(map[string]*ScheduledTask),
	cache:    &configCache{data: make(map[string]*model.SchedulerConfig)},
	stopChan: make(chan struct{}),
}

// RegisterScheduledTask registers a task with the global scheduler.
// Must be called before Start().
func RegisterScheduledTask(name string, requiresMaster bool, handler func(ctx context.Context) error) {
	globalScheduler.mu.Lock()
	defer globalScheduler.mu.Unlock()
	globalScheduler.tasks[name] = &ScheduledTask{
		Name:           name,
		RequiresMaster: requiresMaster,
		Handler:        handler,
	}
	logger.LogInfo(context.Background(), fmt.Sprintf("[scheduler] registered task: %s (master-only=%v)", name, requiresMaster))
}

// StartTaskScheduler starts the global scheduler loop. Call once from main().
func StartTaskScheduler() {
	globalScheduler.mu.Lock()
	if globalScheduler.running {
		globalScheduler.mu.Unlock()
		return
	}
	globalScheduler.running = true
	globalScheduler.mu.Unlock()

	go globalScheduler.loop()
	logger.LogInfo(context.Background(), fmt.Sprintf("[scheduler] started (node=%s)", getNodeID()))
}

// StopTaskScheduler gracefully stops the scheduler.
func StopTaskScheduler() {
	globalScheduler.mu.Lock()
	defer globalScheduler.mu.Unlock()
	if globalScheduler.running {
		close(globalScheduler.stopChan)
		globalScheduler.running = false
	}
}

// TriggerTask forces immediate execution of a named task (for admin use).
func TriggerTask(taskName string) error {
	globalScheduler.mu.RLock()
	task, ok := globalScheduler.tasks[taskName]
	globalScheduler.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task not registered: %s", taskName)
	}
	cfg, err := globalScheduler.loadConfig(task.Name)
	if err != nil {
		return fmt.Errorf("config not found for %s: %w", taskName, err)
	}
	return globalScheduler.runTask(task, cfg)
}

// GetTaskSchedulerStatus returns current scheduler config for all tasks.
func GetTaskSchedulerStatus() ([]model.SchedulerConfig, error) {
	return model.GetAllSchedulerConfigs()
}

// ─── internal ────────────────────────────────────────────────────────────────

func (s *TaskScheduler) loop() {
	ticker := time.NewTicker(schedulerPollInterval)
	defer ticker.Stop()

	// Force a config refresh immediately on startup.
	_ = s.cache.refresh()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *TaskScheduler) tick() {
	s.mu.RLock()
	tasks := make([]*ScheduledTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()

	for _, task := range tasks {
		cfg, err := s.loadConfig(task.Name)
		if err != nil {
			// Config row missing — skip silently (will be created on next upsert).
			continue
		}
		if cfg.Enabled != 1 {
			continue
		}
		if time.Now().Unix() < cfg.NextRunTime {
			continue
		}

		if task.RequiresMaster {
			// Lock TTL = timeout + 30 s buffer so no other node steals while running.
			lockKey := "sched_" + task.Name
			ttl := cfg.TimeoutSeconds + 30
			if err := RunWithLock(lockKey, ttl, func() error {
				return s.runTask(task, cfg)
			}); err != nil {
				logger.LogWarn(context.Background(),
					fmt.Sprintf("[scheduler] run or lock error for %s: %v", task.Name, err))
			}
		} else {
			_ = s.runTask(task, cfg)
		}
	}
}

func (s *TaskScheduler) loadConfig(taskName string) (*model.SchedulerConfig, error) {
	if cfg, ok := s.cache.get(taskName); ok {
		return cfg, nil
	}
	// Cache stale — reload all.
	if err := s.cache.refresh(); err != nil {
		// Try direct DB fetch as fallback.
		return model.GetSchedulerConfig(taskName)
	}
	if cfg, ok := s.cache.get(taskName); ok {
		return cfg, nil
	}
	return model.GetSchedulerConfig(taskName)
}

func (s *TaskScheduler) runTask(task *ScheduledTask, cfg *model.SchedulerConfig) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.TimeoutSeconds)*time.Second,
	)
	defer cancel()

	start := time.Now()
	logger.LogInfo(ctx, fmt.Sprintf("[scheduler] starting task: %s", task.Name))

	runErr := task.Handler(ctx)

	duration := time.Since(start)
	status := "success"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
		logger.LogWarn(ctx, fmt.Sprintf("[scheduler] task %s failed after %s: %v", task.Name, duration, runErr))
	} else {
		logger.LogInfo(ctx, fmt.Sprintf("[scheduler] task %s done in %s", task.Name, duration))
	}

	nextRun := time.Now().Unix() + int64(cfg.IntervalSeconds)
	updates := map[string]interface{}{
		"last_run_time":   start.Unix(),
		"last_run_status": status,
		"last_error":      errMsg,
		"next_run_time":   nextRun,
	}
	if uErr := model.UpdateSchedulerConfigFields(task.Name, updates); uErr != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[scheduler] failed to update config for %s: %v", task.Name, uErr))
		if runErr == nil {
			runErr = uErr
		}
	}
	// Invalidate cache so next tick reads fresh next_run_time.
	s.cache.mu.Lock()
	s.cache.fetchedAt = time.Time{}
	s.cache.mu.Unlock()
	return runErr
}
