package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const businessStatsDeadLetterFile = "business_stats_dead_letter.log"

var businessStatsDeadLetterLock sync.Mutex

func writeBusinessStatsDeadLetter(kind, reason string, attempts int, payload any) {
	record := map[string]any{
		"time":     time.Now().Format(time.RFC3339),
		"kind":     kind,
		"reason":   reason,
		"attempts": attempts,
		"payload":  payload,
	}
	data, err := common.Marshal(record)
	if err != nil {
		common.SysError("writeBusinessStatsDeadLetter: marshal failed: " + err.Error())
		return
	}

	logDir := "."
	if common.LogDir != nil && *common.LogDir != "" {
		logDir = *common.LogDir
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		common.SysError("writeBusinessStatsDeadLetter: mkdir failed: " + err.Error())
		return
	}

	path := filepath.Join(logDir, businessStatsDeadLetterFile)
	businessStatsDeadLetterLock.Lock()
	defer businessStatsDeadLetterLock.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		common.SysError("writeBusinessStatsDeadLetter: open failed: " + err.Error())
		return
	}
	defer file.Close()
	if _, err := fmt.Fprintln(file, string(data)); err != nil {
		common.SysError("writeBusinessStatsDeadLetter: write failed: " + err.Error())
	}
}
