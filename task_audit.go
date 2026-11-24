package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type AuditRecord struct {
	Timestamp string `json:"timestamp"`
	Operator  string `json:"operator"`
	Action    string `json:"action"`
	TaskName  string `json:"taskName"`
	Success   bool   `json:"success"`
	Details   string `json:"details,omitempty"`
	Error     string `json:"error,omitempty"`
}

type AuditLogger struct {
	mu             sync.Mutex
	logDir         string
	customFilePath string
	queue          chan AuditRecord
}

var (
	globalAuditLogger *AuditLogger
	auditOnce         sync.Once
)

// defaultAuditLogDir 返回默认审计日志根目录（exe 目录下的 logs/，或当前工作目录下的 logs/）。
func defaultAuditLogDir() string {
	exePath, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exePath), "logs")
	}
	return "logs"
}

// monthlyAuditLogPath 根据指定基准目录与时间生成按月拆分的日志文件名，例如 audit_2026-11.jsonl。
func monthlyAuditLogPath(baseDir string, t time.Time) string {
	if baseDir == "" {
		baseDir = defaultAuditLogDir()
	}
	monthStr := t.Format("2006-01")
	return filepath.Join(baseDir, fmt.Sprintf("audit_%s.jsonl", monthStr))
}

// defaultAuditLogPath 返回当前月份的默认审计日志文件路径。
func defaultAuditLogPath() string {
	return monthlyAuditLogPath(defaultAuditLogDir(), time.Now())
}

func initAuditLogger(logPathOrDir string) *AuditLogger {
	var dir, customFile string
	if logPathOrDir == "" {
		dir = defaultAuditLogDir()
	} else if strings.HasSuffix(strings.ToLower(logPathOrDir), ".jsonl") {
		customFile = logPathOrDir
		dir = filepath.Dir(logPathOrDir)
	} else {
		dir = logPathOrDir
	}

	al := &AuditLogger{
		logDir:         dir,
		customFilePath: customFile,
		queue:          make(chan AuditRecord, 256),
	}
	go al.worker()
	globalAuditLogger = al
	return al
}

func getGlobalAuditLogger() *AuditLogger {
	auditOnce.Do(func() {
		if globalAuditLogger == nil {
			initAuditLogger("")
		}
	})
	return globalAuditLogger
}

func (al *AuditLogger) worker() {
	for record := range al.queue {
		al.writeToFile(record)
	}
}

func (al *AuditLogger) writeToFile(record AuditRecord) {
	al.mu.Lock()
	defer al.mu.Unlock()

	targetPath := al.customFilePath
	if targetPath == "" {
		targetPath = monthlyAuditLogPath(al.logDir, time.Now())
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debugLog("create audit log dir failed: %v", err)
		return
	}
	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		debugLog("open audit log failed: %v", err)
		return
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err == nil {
		data = append(data, '\n')
		_, _ = f.Write(data)
	}
}

func (al *AuditLogger) Record(action, taskName string, success bool, details, errMessage string) {
	operator := os.Getenv("USERNAME")
	if domain := os.Getenv("USERDOMAIN"); domain != "" && operator != "" {
		operator = domain + "\\" + operator
	}
	if operator == "" {
		operator = "SYSTEM"
	}
	record := AuditRecord{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Operator:  operator,
		Action:    action,
		TaskName:  taskName,
		Success:   success,
		Details:   details,
		Error:     errMessage,
	}
	select {
	case al.queue <- record:
	default:
		go al.writeToFile(record)
	}
}

// recordAudit 提供全局快速打点调用。
func recordAudit(action, taskName string, success bool, details, errMessage string) {
	if al := getGlobalAuditLogger(); al != nil {
		al.Record(action, taskName, success, details, errMessage)
	}
}

// findAuditLogFiles 扫描目录下所有按月拆分的审计日志文件及旧版日志，按时间最新倒序返回。
func findAuditLogFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var monthlyFiles []string
	hasLegacy := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".jsonl") {
			continue
		}
		if lower == "audit.jsonl" {
			hasLegacy = true
			continue
		}
		if strings.HasPrefix(lower, "audit_") || strings.HasPrefix(lower, "audit-") {
			monthlyFiles = append(monthlyFiles, name)
		}
	}

	// 降序排序，如 audit_2026-12.jsonl 在 audit_2026-11.jsonl 之前
	sort.Slice(monthlyFiles, func(i, j int) bool {
		return monthlyFiles[i] > monthlyFiles[j]
	})

	var result []string
	for _, name := range monthlyFiles {
		result = append(result, filepath.Join(dir, name))
	}
	if hasLegacy {
		result = append(result, filepath.Join(dir, "audit.jsonl"))
	}
	return result
}

// readRecentAuditRecords 从审计日志中倒序读取最近条目，支持跨月份文件聚合与按任务名过滤。
func readRecentAuditRecords(logPathOrDir, taskName string, limit int) ([]AuditRecord, error) {
	var filePaths []string

	if logPathOrDir == "" {
		if globalAuditLogger != nil && globalAuditLogger.customFilePath != "" {
			filePaths = []string{globalAuditLogger.customFilePath}
		} else {
			dir := defaultAuditLogDir()
			if globalAuditLogger != nil && globalAuditLogger.logDir != "" {
				dir = globalAuditLogger.logDir
			}
			filePaths = findAuditLogFiles(dir)
		}
	} else {
		fi, err := os.Stat(logPathOrDir)
		if err == nil && fi.IsDir() {
			filePaths = findAuditLogFiles(logPathOrDir)
		} else {
			filePaths = []string{logPathOrDir}
		}
	}

	if len(filePaths) == 0 {
		return nil, nil
	}

	var records []AuditRecord
	filterName := strings.ToLower(strings.TrimSpace(taskName))

	// 遍历文件（最新月份优先）
	for _, fPath := range filePaths {
		data, err := os.ReadFile(fPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		// 单文件内倒序读取（最新行优先）
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			var r AuditRecord
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				continue
			}
			if filterName != "" {
				rName := strings.ToLower(strings.TrimSpace(r.TaskName))
				if !strings.Contains(rName, filterName) && !strings.Contains(filterName, rName) {
					continue
				}
			}
			records = append(records, r)
			if limit > 0 && len(records) >= limit {
				return records, nil
			}
		}
	}

	return records, nil
}

// exportAuditRecordsToCSV 将操作审计记录导出为带 UTF-8 BOM 的标准 CSV 文件。
func exportAuditRecordsToCSV(records []AuditRecord, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	w := csv.NewWriter(file)
	defer w.Flush()

	header := []string{"序号", "时间", "操作人", "操作动作", "目标任务", "执行结果", "详细说明", "错误信息"}
	if err := w.Write(header); err != nil {
		return err
	}

	for i, r := range records {
		res := "成功"
		if !r.Success {
			res = "失败"
		}
		row := []string{
			fmt.Sprintf("%d", i+1),
			r.Timestamp,
			r.Operator,
			r.Action,
			r.TaskName,
			res,
			r.Details,
			r.Error,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// normalizeAuditTaskKey 规范化任务完整路径作为审计策略索引键。
func normalizeAuditTaskKey(taskName string) string {
	s := strings.ReplaceAll(strings.TrimSpace(taskName), "/", `\`)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, `\`) {
		s = `\` + s
	}
	return strings.ToLower(s)
}

// isSystemTask 判断任务是否属于系统级内置定时任务（例如 \Microsoft\ 下或使用系统内置资源作者的任务）。
// 用户自己在根目录或自定义文件夹设置的定时任务返回 false。
func isSystemTask(t Task) bool {
	name := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(t.Name, "/", `\`)))
	if name != "" && !strings.HasPrefix(name, `\`) {
		name = `\` + name
	}
	loc := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(t.Location, "/", `\`)))
	if loc != "" && !strings.HasPrefix(loc, `\`) {
		loc = `\` + loc
	}
	if strings.HasPrefix(name, `\microsoft\`) || loc == `\microsoft` || strings.HasPrefix(loc, `\microsoft\`) {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(t.Author), "$(@") {
		return true
	}
	return false
}

type auditPolicyFileData struct {
	Overrides map[string]bool `json:"overrides"`
}

// AuditPolicyStore 管理单任务级别的日志审计开关覆盖配置，持久化存储于 audit_policy.json。
type AuditPolicyStore struct {
	mu        sync.RWMutex
	filePath  string
	overrides map[string]bool
}

var (
	globalAuditPolicy *AuditPolicyStore
	auditPolicyOnce   sync.Once
)

func defaultAuditPolicyPath() string {
	dir := defaultAuditLogDir()
	if globalAuditLogger != nil && globalAuditLogger.logDir != "" {
		dir = globalAuditLogger.logDir
	}
	return filepath.Join(dir, "audit_policy.json")
}

func newAuditPolicyStore(filePath string) *AuditPolicyStore {
	if filePath == "" {
		filePath = defaultAuditPolicyPath()
	}
	store := &AuditPolicyStore{
		filePath:  filePath,
		overrides: make(map[string]bool),
	}
	store.load()
	return store
}

func getGlobalAuditPolicyStore() *AuditPolicyStore {
	auditPolicyOnce.Do(func() {
		if globalAuditPolicy == nil {
			globalAuditPolicy = newAuditPolicyStore("")
		}
	})
	return globalAuditPolicy
}

func (s *AuditPolicyStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var parsed auditPolicyFileData
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	if parsed.Overrides != nil {
		for k, v := range parsed.Overrides {
			if norm := normalizeAuditTaskKey(k); norm != "" {
				s.overrides[norm] = v
			}
		}
	}
}

func (s *AuditPolicyStore) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	payload := auditPolicyFileData{
		Overrides: s.overrides,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.filePath, data, 0644)
}

// IsAuditEnabled 返回任务当前是否启用了日志审计。
// 自定义任务默认全部开启；系统级任务默认关闭，除非用户手动覆盖开启。
func (s *AuditPolicyStore) IsAuditEnabled(t Task) bool {
	key := normalizeAuditTaskKey(t.Name)
	s.mu.RLock()
	val, ok := s.overrides[key]
	s.mu.RUnlock()
	if ok {
		return val
	}
	return !isSystemTask(t)
}

// StatusLabel 返回任务在界面列表与详情中展示的日志审计状态文案。
func (s *AuditPolicyStore) StatusLabel(t Task) string {
	key := normalizeAuditTaskKey(t.Name)
	s.mu.RLock()
	val, ok := s.overrides[key]
	s.mu.RUnlock()
	if ok {
		if val {
			return "已开启(手动)"
		}
		return "已关闭(手动)"
	}
	if isSystemTask(t) {
		return "未开启(系统默认)"
	}
	return "已开启(默认)"
}

// SetTaskAudit 设置并持久化指定任务的日志审计开关覆盖状态。
func (s *AuditPolicyStore) SetTaskAudit(taskName string, enabled bool) error {
	key := normalizeAuditTaskKey(taskName)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[key] = enabled
	return s.saveLocked()
}

// RenameTask 在任务重命名时迁移其手动审计覆盖记录。
func (s *AuditPolicyStore) RenameTask(oldName, newName string) error {
	oldKey := normalizeAuditTaskKey(oldName)
	newKey := normalizeAuditTaskKey(newName)
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	val, ok := s.overrides[oldKey]
	if !ok {
		return nil
	}
	delete(s.overrides, oldKey)
	s.overrides[newKey] = val
	return s.saveLocked()
}

// DeleteTask 在删除任务时清理其手动审计覆盖记录。
func (s *AuditPolicyStore) DeleteTask(taskName string) error {
	key := normalizeAuditTaskKey(taskName)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.overrides[key]; !ok {
		return nil
	}
	delete(s.overrides, key)
	return s.saveLocked()
}

func isTaskAuditEnabled(t Task) bool {
	return getGlobalAuditPolicyStore().IsAuditEnabled(t)
}

func taskAuditStatusText(t Task) string {
	return getGlobalAuditPolicyStore().StatusLabel(t)
}

func setTaskAuditEnabled(taskName string, enabled bool) error {
	return getGlobalAuditPolicyStore().SetTaskAudit(taskName, enabled)
}
