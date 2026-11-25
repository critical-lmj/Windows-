package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMonitorInterval = 10 * time.Second
	monitorStateFileName   = "monitor_state.json"
)

// TaskSnapshot 记录上一次轮询时某个定时任务的关键运行快照，用于无事件日志的增量变更检测。
type TaskSnapshot struct {
	TaskName   string `json:"taskName"`
	Status     string `json:"status"`
	LastRun    string `json:"lastRun"`
	LastResult string `json:"lastResult"`
	RunningAt  string `json:"runningAt,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
}

type monitorStateFileData struct {
	UpdatedAt string                  `json:"updatedAt"`
	Tasks     map[string]TaskSnapshot `json:"tasks"`
}

var execLogWriteMu sync.Mutex

// dailyExecLogPath 返回指定日期的独立运行日志文件路径，按天拆分格式为 logs/exec_YYYY-MM-DD.jsonl。
func dailyExecLogPath(baseDir string, t time.Time) string {
	if baseDir == "" {
		baseDir = defaultAuditLogDir()
	}
	return filepath.Join(baseDir, fmt.Sprintf("exec_%s.jsonl", t.Format("2006-01-02")))
}

// findExecLogFiles 扫描目录下所有按天拆分的运行日志文件，按日期降序返回（最新一天排在最前）。
func findExecLogFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".jsonl") {
			continue
		}
		if strings.HasPrefix(lower, "exec_") || strings.HasPrefix(lower, "exec-") {
			files = append(files, name)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i] > files[j]
	})
	result := make([]string, 0, len(files))
	for _, name := range files {
		result = append(result, filepath.Join(dir, name))
	}
	return result
}

// appendExecRecordToDir 将一条任务执行记录追加写入对应自然日的 JSONL 文件中。
func appendExecRecordToDir(logDir string, rec TaskExecutionRecord, now time.Time) error {
	if logDir == "" {
		logDir = defaultAuditLogDir()
	}
	if now.IsZero() {
		now = time.Now()
	}
	targetPath := dailyExecLogPath(logDir, now)

	execLogWriteMu.Lock()
	defer execLogWriteMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// readRecentExecRecords 从本地按天运行日志文件（logs/exec_YYYY-MM-DD.jsonl）跨天倒序聚合读取记录。
// 同一 InstanceID 若同时存在早先的“运行中”记录和后续的“完成/成功/失败”记录，仅保留最新一条。
// limit <= 0 表示不限制数量，返回全部匹配记录。
func readRecentExecRecords(logDir, taskName string, limit int) ([]TaskExecutionRecord, error) {
	if logDir == "" {
		logDir = defaultAuditLogDir()
	}
	filePaths := findExecLogFiles(logDir)
	if len(filePaths) == 0 {
		return nil, nil
	}

	filterKey := normalizeAuditTaskKey(taskName)
	seenInstances := make(map[string]bool)
	var records []TaskExecutionRecord

	for _, fPath := range filePaths {
		data, err := os.ReadFile(fPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			var r TaskExecutionRecord
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				continue
			}
			if filterKey != "" {
				recKey := normalizeAuditTaskKey(r.TaskName)
				if recKey != filterKey && !strings.Contains(recKey, filterKey) {
					continue
				}
			}
			if r.InstanceID != "" {
				if seenInstances[r.InstanceID] {
					continue
				}
				seenInstances[r.InstanceID] = true
			}
			records = append(records, r)
			if limit > 0 && len(records) >= limit {
				return records, nil
			}
		}
	}
	return records, nil
}

func isTaskStatusRunning(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "running" || strings.Contains(s, "正在运行") || strings.Contains(s, "运行中")
}

func isValidTaskLastRun(lastRun string) bool {
	s := strings.TrimSpace(lastRun)
	if s == "" || s == "-" || s == "—" || s == "0" || strings.EqualFold(s, "N/A") {
		return false
	}
	if strings.Contains(s, "从未") || strings.HasPrefix(s, "1999/11/30") || strings.HasPrefix(s, "1999-11-30") {
		return false
	}
	return true
}

func normalizeExecDisplayTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if !isValidTaskLastRun(raw) {
		return ""
	}
	for _, layout := range []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05",
		"2006/1/2 15:04:05",
		"2006-1-2 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02 15:04",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			if t.Year() <= 1999 {
				return ""
			}
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return raw
}

func parseExecDisplayTime(value string) time.Time {
	value = normalizeExecDisplayTime(value)
	if value == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func statusFromExitCode(exitCode string) string {
	code := strings.TrimSpace(exitCode)
	lower := strings.ToLower(code)
	switch lower {
	case "", "n/a", "-", "—":
		return "完成"
	case "0", "0x0", "0x00000000":
		return "成功"
	case "267008", "0x41300":
		return "完成"
	case "267009", "0x41301":
		return "运行中"
	case "267014", "0x41306":
		return "已终止 (" + code + ")"
	default:
		return "失败 (" + code + ")"
	}
}

func formatMonitorDuration(d time.Duration) string {
	if d < 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func makeExecInstanceID(taskKey, startStr string, now time.Time) string {
	if startStr != "" {
		return fmt.Sprintf("%s|%s", taskKey, startStr)
	}
	return fmt.Sprintf("%s|%d", taskKey, now.UnixNano())
}

// diffTaskSnapshots 对比前后两次全量任务快照，识别新启动、执行结束及轮询间隔内完成的任务，
// 输出最新快照和需要落盘的独立执行记录。记录范围覆盖本机全部定时任务。
func diffTaskSnapshots(prev map[string]TaskSnapshot, current []Task, now time.Time, hasBaseline bool) (map[string]TaskSnapshot, []TaskExecutionRecord) {
	next := make(map[string]TaskSnapshot, len(current))
	var emitted []TaskExecutionRecord
	nowFormatted := now.Format("2006-01-02 15:04:05")

	for _, task := range current {
		key := normalizeAuditTaskKey(task.Name)
		if key == "" {
			continue
		}
		curRunning := isTaskStatusRunning(task.Status)
		curLastRun := strings.TrimSpace(task.LastRunTime)
		curLastRunValid := isValidTaskLastRun(curLastRun)
		curLastResult := strings.TrimSpace(task.LastResult)

		snap := TaskSnapshot{
			TaskName:   task.Name,
			Status:     task.Status,
			LastRun:    curLastRun,
			LastResult: curLastResult,
		}

		if !hasBaseline {
			if curRunning {
				startStr := normalizeExecDisplayTime(curLastRun)
				if startStr == "" {
					startStr = nowFormatted
				}
				instID := makeExecInstanceID(key, startStr, now)
				snap.RunningAt = now.Format(time.RFC3339Nano)
				snap.InstanceID = instID
				emitted = append(emitted, TaskExecutionRecord{
					InstanceID: instID,
					TaskName:   task.Name,
					Action:     task.TaskToRun,
					StartTime:  startStr,
					Duration:   "运行中",
					Status:     "运行中",
				})
			}
			next[key] = snap
			continue
		}

		old, existed := prev[key]
		oldRunning := existed && isTaskStatusRunning(old.Status)
		lastRunChanged := curLastRunValid && (!existed || curLastRun != old.LastRun)

		switch {
		case curRunning && (!oldRunning || lastRunChanged):
			// 若前一次尚处于运行中且本轮已切换为新的 LastRunTime，先补写上一次的完成记录
			if oldRunning && lastRunChanged && old.InstanceID != "" {
				oldStart := normalizeExecDisplayTime(old.LastRun)
				if oldStart == "" && old.RunningAt != "" {
					if rt, err := time.Parse(time.RFC3339Nano, old.RunningAt); err == nil {
						oldStart = rt.Local().Format("2006-01-02 15:04:05")
					}
				}
				dur := "-"
				if old.RunningAt != "" {
					if rt, err := time.Parse(time.RFC3339Nano, old.RunningAt); err == nil {
						dur = formatMonitorDuration(now.Sub(rt))
					}
				}
				emitted = append(emitted, TaskExecutionRecord{
					InstanceID: old.InstanceID,
					TaskName:   task.Name,
					Action:     task.TaskToRun,
					StartTime:  oldStart,
					EndTime:    nowFormatted,
					Duration:   dur,
					ExitCode:   old.LastResult,
					Status:     "完成",
				})
			}
			startStr := normalizeExecDisplayTime(curLastRun)
			if startStr == "" {
				startStr = nowFormatted
			}
			instID := makeExecInstanceID(key, startStr, now)
			snap.RunningAt = now.Format(time.RFC3339Nano)
			snap.InstanceID = instID
			emitted = append(emitted, TaskExecutionRecord{
				InstanceID: instID,
				TaskName:   task.Name,
				Action:     task.TaskToRun,
				StartTime:  startStr,
				Duration:   "运行中",
				Status:     "运行中",
			})

		case curRunning && oldRunning && !lastRunChanged:
			snap.RunningAt = old.RunningAt
			snap.InstanceID = old.InstanceID

		case !curRunning && oldRunning:
			startStr := normalizeExecDisplayTime(curLastRun)
			dur := "-"
			if old.RunningAt != "" {
				if rt, err := time.Parse(time.RFC3339Nano, old.RunningAt); err == nil {
					if startStr == "" {
						startStr = rt.Local().Format("2006-01-02 15:04:05")
					}
					dur = formatMonitorDuration(now.Sub(rt))
				}
			}
			if dur == "-" && startStr != "" {
				if st := parseExecDisplayTime(startStr); !st.IsZero() && !now.Before(st) && now.Sub(st) < 24*time.Hour {
					dur = formatMonitorDuration(now.Sub(st))
				}
			}
			instID := old.InstanceID
			if instID == "" {
				instID = makeExecInstanceID(key, startStr, now)
			}
			status := statusFromExitCode(curLastResult)
			if status == "运行中" {
				status = "完成"
			}
			emitted = append(emitted, TaskExecutionRecord{
				InstanceID: instID,
				TaskName:   task.Name,
				Action:     task.TaskToRun,
				StartTime:  startStr,
				EndTime:    nowFormatted,
				Duration:   dur,
				ExitCode:   curLastResult,
				Status:     status,
			})

		case !curRunning && !oldRunning && lastRunChanged:
			startStr := normalizeExecDisplayTime(curLastRun)
			instID := makeExecInstanceID(key, startStr, now)
			dur := "-"
			if st := parseExecDisplayTime(startStr); !st.IsZero() && !now.Before(st) && now.Sub(st) <= 5*time.Minute {
				dur = formatMonitorDuration(now.Sub(st))
			}
			status := statusFromExitCode(curLastResult)
			if status == "运行中" {
				status = "完成"
			}
			emitted = append(emitted, TaskExecutionRecord{
				InstanceID: instID,
				TaskName:   task.Name,
				Action:     task.TaskToRun,
				StartTime:  startStr,
				EndTime:    nowFormatted,
				Duration:   dur,
				ExitCode:   curLastResult,
				Status:     status,
			})
		}

		next[key] = snap
	}

	return next, emitted
}

func loadMonitorState(statePath string) (map[string]TaskSnapshot, bool) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return make(map[string]TaskSnapshot), false
	}
	var parsed monitorStateFileData
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Tasks == nil {
		return make(map[string]TaskSnapshot), false
	}
	return parsed.Tasks, true
}

func saveMonitorState(statePath string, tasks map[string]TaskSnapshot, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return err
	}
	payload := monitorStateFileData{
		UpdatedAt: now.Format(time.RFC3339),
		Tasks:     tasks,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := statePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	_ = os.Remove(statePath)
	if err := os.Rename(tmpPath, statePath); err != nil {
		// Windows 上某些场景 Rename 失败时回退直接覆写
		_ = os.Remove(tmpPath)
		return os.WriteFile(statePath, data, 0644)
	}
	return nil
}

// TaskExecMonitor 负责定时轮询本机全部定时任务状态并将变化落盘至按天日志文件。
type TaskExecMonitor struct {
	mu          sync.Mutex
	logDir      string
	statePath   string
	interval    time.Duration
	queryFn     func() ([]Task, error)
	nowFn       func() time.Time
	prev        map[string]TaskSnapshot
	hasBaseline bool
	stopCh      chan struct{}
	doneCh      chan struct{}
	running     bool
}

func newTaskExecMonitor(logDir string, interval time.Duration, queryFn func() ([]Task, error)) *TaskExecMonitor {
	if logDir == "" {
		logDir = defaultAuditLogDir()
	}
	if interval <= 0 {
		interval = defaultMonitorInterval
	}
	if queryFn == nil {
		queryFn = queryTasks
	}
	statePath := filepath.Join(logDir, monitorStateFileName)
	prev, hasBaseline := loadMonitorState(statePath)
	return &TaskExecMonitor{
		logDir:      logDir,
		statePath:   statePath,
		interval:    interval,
		queryFn:     queryFn,
		nowFn:       time.Now,
		prev:        prev,
		hasBaseline: hasBaseline,
	}
}

// PollOnce 执行一次全量任务状态采集、差异比对并按天落盘。
func (m *TaskExecMonitor) PollOnce() (int, error) {
	tasks, err := m.queryFn()
	if err != nil {
		return 0, err
	}
	now := m.nowFn()

	m.mu.Lock()
	next, emitted := diffTaskSnapshots(m.prev, tasks, now, m.hasBaseline)
	m.prev = next
	m.hasBaseline = true
	m.mu.Unlock()

	for _, rec := range emitted {
		if writeErr := appendExecRecordToDir(m.logDir, rec, now); writeErr != nil {
			debugLog("append exec record failed for %s: %v", rec.TaskName, writeErr)
		}
	}
	if err := saveMonitorState(m.statePath, next, now); err != nil {
		debugLog("save monitor state failed: %v", err)
	}
	return len(emitted), nil
}

// Start 在独立后台协程中串行执行定时采集循环。
func (m *TaskExecMonitor) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	m.running = true
	m.mu.Unlock()

	go func() {
		defer close(m.doneCh)
		defer func() {
			if r := recover(); r != nil {
				debugLog("panic in TaskExecMonitor loop: %v\n%s", r, string(debug.Stack()))
			}
		}()

		if count, err := m.PollOnce(); err != nil {
			debugLog("initial monitor poll failed: %v", err)
		} else {
			debugLog("initial monitor poll completed: %d records emitted", count)
		}

		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				if count, err := m.PollOnce(); err != nil {
					debugLog("monitor poll failed: %v", err)
				} else if count > 0 {
					debugLog("monitor poll emitted %d records", count)
				}
			}
		}
	}()
}

// Stop 优雅停止后台轮询循环。
func (m *TaskExecMonitor) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	close(m.stopCh)
	done := m.doneCh
	m.mu.Unlock()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
