package model

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
)

const requestLogRecoveryFile = ".request-log-recovery.json"
const requestLogRecoveryTemp = ".request-log-recovery.tmp"
const requestLogRecoveryMaxRecord = 256 << 20

// Positions are committed byte offsets, updated only by asynchronous log writers
// under reqLogMu. A checkpoint makes intentional removals survive a restart while
// allowing records written after the checkpoint to be recovered from the tail.
var requestLogCommitted = make(map[string]int64)
var requestLogRecoveryIncomplete atomic.Bool // explicit deletion may repair incomplete recovery

type requestLogRecoveryCheckpoint struct {
	Version  int                       `json:"version"`
	Sequence int64                     `json:"sequence"`
	Files    map[string]int64          `json:"files"`
	Entries  []requestLogSnapshotEntry `json:"entries"`
}

// saveRequestLogCheckpointLocked runs only at shutdown or index eviction/deletion.
// The caller holds reqLogMu so no committed record can fall between entries and
// file positions. Never call it from the relay goroutine.
func saveRequestLogCheckpointLocked(items []requestLogIndexEntry) error {
	if requestLogRecoveryIncomplete.Load() {
		return errors.New("request log recovery is incomplete")
	}
	return writeRequestLogCheckpointLocked(items, requestLogCommitted)
}

// saveRequestLogDeletionCheckpointLocked lets an explicit deletion replace an
// untrusted recovery checkpoint without replaying old bodies. The file inventory
// records deletion boundaries even for files recovery could not parse.
func saveRequestLogDeletionCheckpointLocked(items []requestLogIndexEntry) error {
	if !requestLogRecoveryIncomplete.Load() {
		return saveRequestLogCheckpointLocked(items)
	}
	if !RequestLogStoreReady() {
		return errRequestLogStoreUnavailable
	}
	root, err := os.OpenRoot(requestLogRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	active, snapSeq := requestLogSegmentSnapshot()
	recovery := requestLogDiskRecovery{root: root, files: make(map[string]int64), positionsOnly: true}
	recovery.scanDir(".", 0)
	if recovery.incomplete {
		return errors.New("request log file inventory is incomplete")
	}
	for name := range recovery.files {
		base := name[strings.LastIndexByte(name, '/')+1:]
		if keepRequestLogSegment(name, base, active, snapSeq) {
			// A writer can have written bytes while waiting to acquire reqLogMu.
			// Only its committed prefix belongs to the deletion; recover the tail.
			recovery.files[name] = requestLogCommitted[name]
		}
	}
	if err := writeRequestLogCheckpointLocked(items, recovery.files); err != nil {
		return err
	}
	requestLogCommitted = recovery.files
	requestLogRecoveryIncomplete.Store(false)
	return nil
}

// writeRequestLogCheckpointLocked persists the target index and its recovery
// boundaries before callers commit any deletion to memory.
func writeRequestLogCheckpointLocked(items []requestLogIndexEntry, files map[string]int64) error {
	if !RequestLogStoreReady() {
		return errRequestLogStoreUnavailable
	}
	root, err := os.OpenRoot(requestLogRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	checkpoint := requestLogRecoveryCheckpoint{Version: 1, Sequence: atomic.LoadInt64(&reqLogSeq), Files: make(map[string]int64)}
	for name, offset := range files {
		if _, err := root.Stat(name); os.IsNotExist(err) {
			delete(files, name)
			continue
		}
		checkpoint.Files[name] = offset
	}
	for _, item := range items {
		checkpoint.Entries = append(checkpoint.Entries, requestLogSnapshotEntry{Meta: item.meta, Rel: item.rel})
	}
	data, err := common.Marshal(checkpoint)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(requestLogRecoveryTemp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(requestLogRecoveryTemp, requestLogRecoveryFile)
}

func CheckpointRequestLogs() {
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	if err := saveRequestLogCheckpointLocked(reqLogItems); err != nil {
		common.SysError("request log checkpoint failed: " + err.Error())
	}
}

type requestLogDiskRecovery struct {
	root          *os.Root
	items         []requestLogIndexEntry
	files         map[string]int64
	sequence      int64
	count         int
	limit         int
	incomplete    bool
	positionsOnly bool
}

func (r *requestLogDiskRecovery) fail(err error) {
	r.incomplete = true
	common.SysError("request log recovery: " + err.Error())
}

func (r *requestLogDiskRecovery) retain(entry requestLogIndexEntry) {
	r.count++
	r.items = append(r.items, entry)
	if int64(entry.meta.Id) > r.sequence {
		r.sequence = int64(entry.meta.Id)
	}
	if len(r.items) > r.limit && len(r.items)-r.limit >= r.limit {
		sort.Slice(r.items, func(i, j int) bool { return r.items[i].meta.CreatedAt < r.items[j].meta.CreatedAt })
		copy(r.items, r.items[len(r.items)-r.limit:])
		clear(r.items[r.limit:])
		r.items = r.items[:r.limit]
	}
}

// scanDir visits only the two supported layouts, never follows symlinks, and
// bounds directory enumeration to 1000 entries at a time.
func (r *requestLogDiskRecovery) scanDir(name string, depth int) {
	dir, err := r.root.Open(name)
	if err != nil {
		r.fail(err)
		return
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(requestLogSweepBatch)
		for _, entry := range entries {
			rel := entry.Name()
			if name != "." {
				rel = name + "/" + rel
			}
			if entry.Type()&os.ModeSymlink != 0 {
				r.fail(fmt.Errorf("skipping symbolic link %s", rel))
				continue
			}
			if depth == 0 {
				if entry.IsDir() && isRequestLogDateDir(entry.Name()) {
					r.scanDir(rel, 1)
				}
				continue
			}
			if entry.IsDir() {
				if depth == 1 && len(entry.Name()) == 1 && entry.Name()[0] >= '0' && entry.Name()[0] <= '7' {
					r.scanDir(rel, 2)
				}
				continue
			}
			if depth == 1 && isRequestLogSegmentName(entry.Name()) {
				r.scanFile(rel, true)
			}
			if depth == 2 && strings.HasSuffix(entry.Name(), ".json") {
				r.scanFile(rel, false)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				r.fail(readErr)
			}
			return
		}
	}
}

func (r *requestLogDiskRecovery) scanFile(name string, segment bool) {
	offset, known := r.files[name]
	if known && offset == -1 {
		return
	} // legacy file already checkpointed
	file, err := r.root.Open(name)
	if err != nil {
		r.fail(err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		r.fail(err)
		return
	}
	if !info.Mode().IsRegular() {
		r.fail(fmt.Errorf("not a regular request log file: %s", name))
		return
	}
	if r.positionsOnly {
		if segment {
			r.files[name] = info.Size()
		} else {
			r.files[name] = -1
		}
		return
	}
	if offset < 0 || offset > info.Size() {
		r.fail(fmt.Errorf("invalid file or checkpoint position: %s", name))
		return
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		r.fail(err)
		return
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		var data []byte
		var readErr error
		if segment {
			for {
				part, err := reader.ReadSlice('\n')
				if len(data)+len(part) > requestLogRecoveryMaxRecord {
					r.files[name] = info.Size()
					common.SysError("request log recovery: skipping oversized record and remaining file tail: " + name)
					return
				}
				data = append(data, part...)
				readErr = err
				if err != bufio.ErrBufferFull {
					break
				}
			}
		} else {
			data, readErr = io.ReadAll(io.LimitReader(reader, requestLogRecoveryMaxRecord+1))
			if len(data) > requestLogRecoveryMaxRecord {
				r.files[name] = -1
				common.SysError("request log recovery: skipping oversized record: " + name)
				return
			}
		}
		if readErr != nil && readErr != io.EOF {
			r.fail(readErr)
			return
		}
		if len(data) == 0 {
			return
		}
		recordOffset := offset
		rel := name
		if segment {
			length := len(data)
			if data[length-1] == '\n' {
				length--
			}
			rel = requestLogRecordRel(name, offset, length)
			offset += int64(len(data))
			r.files[name] = offset
		} else {
			r.files[name] = -1
		}
		var log RequestLog
		if err := common.Unmarshal(data, &log); err != nil || log.Id <= 0 || log.CreatedAt <= 0 {
			// Corruption is local to this record, not a reason to disable every
			// deletion and sweep. Its bytes remain behind the saved boundary.
			common.SysError(fmt.Sprintf("request log recovery: skipping invalid record at %s offset %d", name, recordOffset))
		} else {
			log.Url = common.RedactURIString(log.Url)
			r.retain(requestLogIndexEntry{meta: cloneRequestLogMeta(&log), rel: rel})
		}
		if !segment || readErr == io.EOF {
			return
		}
	}
}

func restoreRequestLogsFromDisk() {
	if !RequestLogStoreReady() {
		return
	}
	root, err := os.OpenRoot(requestLogRoot)
	if err != nil {
		requestLogRecoveryIncomplete.Store(true)
		common.SysError("request log recovery: " + err.Error())
		return
	}
	defer root.Close()
	maxCount, minCount := effectiveRequestLogLimits()
	r := requestLogDiskRecovery{root: root, limit: maxCount, files: make(map[string]int64)}
	file, err := root.Open(requestLogRecoveryFile)
	if err == nil {
		var checkpoint requestLogRecoveryCheckpoint
		err = common.DecodeJson(io.LimitReader(file, requestLogRecoveryMaxRecord), &checkpoint)
		file.Close()
		if err != nil || checkpoint.Version != 1 || checkpoint.Sequence < 0 {
			r.fail(errors.New("invalid recovery checkpoint; retaining all body files"))
		} else {
			r.sequence = checkpoint.Sequence
			for name, offset := range checkpoint.Files {
				if !fs.ValidPath(name) || strings.Contains(name, "\\") || offset < -1 {
					r.fail(errors.New("invalid checkpoint file path"))
					continue
				}
				r.files[name] = offset
			}
			sizes := make(map[string]int64)
			for _, entry := range checkpoint.Entries {
				name, offset, length, segment := parseRequestLogRecordRel(entry.Rel)
				if !segment {
					name = entry.Rel
				}
				if entry.Meta == nil || entry.Meta.Id <= 0 || entry.Meta.CreatedAt <= 0 || !fs.ValidPath(name) || strings.Contains(name, "\\") {
					r.fail(errors.New("invalid checkpoint entry"))
					continue
				}
				size, cached := sizes[name]
				if !cached {
					size = -1
					info, err := root.Stat(name)
					if err == nil && info.Mode().IsRegular() {
						size = info.Size()
					}
					if err != nil && !os.IsNotExist(err) {
						r.fail(err)
					}
					sizes[name] = size
				}
				if size < 0 {
					continue
				}
				if segment && !requestLogRecordWithin(size, offset, length) {
					r.fail(errors.New("checkpoint record outside body file"))
					continue
				}
				entry.Meta.Url = common.RedactURIString(entry.Meta.Url)
				r.retain(requestLogIndexEntry{meta: cloneRequestLogMeta(entry.Meta), rel: entry.Rel})
			}
		}
	} else if !os.IsNotExist(err) {
		r.fail(err)
	}
	// Never reinterpret bodies after a corrupted checkpoint: it might contain
	// deletion boundaries. Preserve files for an operator to repair instead.
	if !r.incomplete {
		r.scanDir(".", 0)
	}
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].meta.CreatedAt < r.items[j].meta.CreatedAt })
	if r.count > maxCount {
		kept := make([]requestLogIndexEntry, minCount)
		copy(kept, r.items[len(r.items)-minCount:])
		r.items = kept
	}
	seen := make(map[int]struct{}, len(r.items))
	for _, item := range r.items {
		if _, duplicate := seen[item.meta.Id]; duplicate {
			r.sequence++
			item.meta.Id = int(r.sequence)
		}
		seen[item.meta.Id] = struct{}{}
	}
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	reqLogItems, requestLogCommitted = r.items, r.files
	atomic.StoreInt64(&reqLogSeq, r.sequence)
	requestLogRecoveryIncomplete.Store(r.incomplete)
	if !r.incomplete {
		if err := saveRequestLogCheckpointLocked(reqLogItems); err != nil {
			common.SysError("request log checkpoint failed: " + err.Error())
		}
	}
	common.SysLog(fmt.Sprintf("request log disk recovery: restored=%d incomplete=%t", len(r.items), r.incomplete))
}
