package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLoggerRecordsAndReads(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logFile := filepath.Join(tmpDir, "test_audit.jsonl")
	al := initAuditLogger(logFile)

	al.Record("CREATE", `\test_task`, true, "创建成功", "")
	al.Record("RUN", `\test_task`, true, "手动触发", "")
	al.Record("DELETE", `\other_task`, false, "删除失败", "拒绝访问")

	// 等待异步队列落盘
	time.Sleep(100 * time.Millisecond)

	records, err := readRecentAuditRecords(logFile, "", 10)
	if err != nil {
		t.Fatalf("readRecentAuditRecords failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// 倒序读取：最新的 DELETE 排在第一位
	if records[0].Action != "DELETE" || records[0].TaskName != `\other_task` || records[0].Success {
		t.Fatalf("unexpected record[0]: %#v", records[0])
	}
	if records[1].Action != "RUN" || !records[1].Success {
		t.Fatalf("unexpected record[1]: %#v", records[1])
	}
	if records[2].Action != "CREATE" || !records[2].Success {
		t.Fatalf("unexpected record[2]: %#v", records[2])
	}

	// 按任务名筛选
	filtered, err := readRecentAuditRecords(logFile, "test_task", 10)
	if err != nil {
		t.Fatalf("readRecentAuditRecords with filter failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered records, got %d", len(filtered))
	}
	for _, r := range filtered {
		if !strings.Contains(r.TaskName, "test_task") {
			t.Fatalf("unexpected filtered record: %#v", r)
		}
	}
}

func TestReadRecentAuditRecordsNonExistentFileReturnsNil(t *testing.T) {
	records, err := readRecentAuditRecords(filepath.Join(os.TempDir(), "non_existent_file.jsonl"), "", 10)
	if err != nil {
		t.Fatalf("expected nil error for non-existent file, got %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(records))
	}
}

func TestExportAuditRecordsToCSV(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "audit.csv")
	records := []AuditRecord{
		{
			Timestamp: "2026-11-24 10:15:00",
			Operator:  "Administrator",
			Action:    "RUN",
			TaskName:  `\run_task`,
			Success:   true,
			Details:   "手动运行",
		},
	}
	err := exportAuditRecordsToCSV(records, tmpFile)
	if err != nil {
		t.Fatalf("exportAuditRecordsToCSV failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}

	// 验证 UTF-8 BOM
	if len(content) < 3 || content[0] != 0xEF || content[1] != 0xBB || content[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM header")
	}

	text := string(content)
	if !strings.Contains(text, "操作动作") || !strings.Contains(text, `\run_task`) || !strings.Contains(text, "手动运行") {
		t.Fatalf("unexpected CSV content: %s", text)
	}
}

func TestMonthlyAuditLogPath(t *testing.T) {
	tm, err := time.Parse("2006-01-02", "2026-11-24")
	if err != nil {
		t.Fatalf("parse time failed: %v", err)
	}
	path := monthlyAuditLogPath("C:\\my_logs", tm)
	want := filepath.Join("C:\\my_logs", "audit_2026-11.jsonl")
	if path != want {
		t.Fatalf("monthlyAuditLogPath() = %q, want %q", path, want)
	}
}

func TestAuditLoggerMonthlySplitAndCrossMonthRead(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "monthly_audit_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 创建历史月份文件 2026-09、2026-10，以及 legacy audit.jsonl
	sepFile := filepath.Join(tmpDir, "audit_2026-09.jsonl")
	octFile := filepath.Join(tmpDir, "audit_2026-10.jsonl")
	legacyFile := filepath.Join(tmpDir, "audit.jsonl")

	_ = os.WriteFile(legacyFile, []byte(`{"timestamp":"2026-08-01 10:00:00","action":"LEGACY_INIT","taskName":"\\legacy","success":true}`+"\n"), 0644)
	_ = os.WriteFile(sepFile, []byte(`{"timestamp":"2026-09-01 10:00:00","action":"SEPT_TASK","taskName":"\\sep","success":true}`+"\n"), 0644)
	_ = os.WriteFile(octFile, []byte(`{"timestamp":"2026-10-01 10:00:00","action":"OCT_TASK","taskName":"\\oct","success":true}`+"\n"), 0644)

	// 使用该目录初始化 logger，其写入应写入当前月份 audit_YYYY-MM.jsonl
	al := initAuditLogger(tmpDir)
	al.Record("NOV_TASK", `\nov`, true, "11月记录", "")

	time.Sleep(100 * time.Millisecond)

	// 验证按目录跨月倒序读取
	records, err := readRecentAuditRecords(tmpDir, "", 10)
	if err != nil {
		t.Fatalf("readRecentAuditRecords failed: %v", err)
	}

	// 预期共有 4 条，按时间最新到最旧排序：NOV_TASK -> OCT_TASK -> SEPT_TASK -> LEGACY_INIT
	if len(records) != 4 {
		t.Fatalf("expected 4 records across months, got %d", len(records))
	}
	if records[0].Action != "NOV_TASK" {
		t.Fatalf("record[0] = %s, want NOV_TASK", records[0].Action)
	}
	if records[1].Action != "OCT_TASK" {
		t.Fatalf("record[1] = %s, want OCT_TASK", records[1].Action)
	}
	if records[2].Action != "SEPT_TASK" {
		t.Fatalf("record[2] = %s, want SEPT_TASK", records[2].Action)
	}
	if records[3].Action != "LEGACY_INIT" {
		t.Fatalf("record[3] = %s, want LEGACY_INIT", records[3].Action)
	}
}

func TestIsSystemTask(t *testing.T) {
	tests := []struct {
		name    string
		task    Task
		wantSys bool
	}{
		{
			name:    "根目录自定义任务",
			task:    Task{Name: `\BackupDatabase`, Location: `\`, Author: "Administrator"},
			wantSys: false,
		},
		{
			name:    "自定义目录且SYSTEM权限的任务",
			task:    Task{Name: `\MyApp\SyncJob`, Location: `\MyApp`, Author: `NT AUTHORITY\SYSTEM`},
			wantSys: false,
		},
		{
			name:    "Microsoft路径系统级任务",
			task:    Task{Name: `\Microsoft\Windows\Defrag\ScheduledDefrag`, Location: `\Microsoft\Windows\Defrag`},
			wantSys: true,
		},
		{
			name:    "大小写混合Microsoft路径",
			task:    Task{Name: `\microsoft\office\Telemetry`, Location: `\microsoft\office`},
			wantSys: true,
		},
		{
			name:    "系统内置MUI资源作者任务",
			task:    Task{Name: `\CustomLocSystemTask`, Location: `\`, Author: `$(@%SystemRoot%\system32\schedsvc.dll,-100)`},
			wantSys: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSystemTask(tt.task); got != tt.wantSys {
				t.Fatalf("isSystemTask(%+v) = %v, want %v", tt.task, got, tt.wantSys)
			}
		})
	}
}

func TestAuditPolicyStoreDefaultAndOverrides(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "audit_policy.json")
	store := newAuditPolicyStore(policyPath)

	customTask := Task{Name: `\UserBackup`, Location: `\`}
	sysTask := Task{Name: `\Microsoft\Windows\DiskCleanup\SilentCleanup`, Location: `\Microsoft\Windows\DiskCleanup`}

	// 默认策略验证
	if !store.IsAuditEnabled(customTask) {
		t.Fatalf("custom task should default to audit enabled")
	}
	if got := store.StatusLabel(customTask); got != "已开启(默认)" {
		t.Fatalf("custom task StatusLabel = %q, want 已开启(默认)", got)
	}
	if store.IsAuditEnabled(sysTask) {
		t.Fatalf("system task should default to audit disabled")
	}
	if got := store.StatusLabel(sysTask); got != "未开启(系统默认)" {
		t.Fatalf("system task StatusLabel = %q, want 未开启(系统默认)", got)
	}

	// 手动开启系统级任务审计，并验证落盘重载
	if err := store.SetTaskAudit(sysTask.Name, true); err != nil {
		t.Fatalf("SetTaskAudit(sysTask, true) failed: %v", err)
	}
	reloaded := newAuditPolicyStore(policyPath)
	if !reloaded.IsAuditEnabled(sysTask) {
		t.Fatalf("reloaded system task should be audit enabled after manual override")
	}
	if got := reloaded.StatusLabel(sysTask); got != "已开启(手动)" {
		t.Fatalf("reloaded system task StatusLabel = %q, want 已开启(手动)", got)
	}

	// 手动关闭自定义任务审计
	if err := reloaded.SetTaskAudit(customTask.Name, false); err != nil {
		t.Fatalf("SetTaskAudit(customTask, false) failed: %v", err)
	}
	if reloaded.IsAuditEnabled(customTask) {
		t.Fatalf("custom task should be disabled after manual override")
	}
	if got := reloaded.StatusLabel(customTask); got != "已关闭(手动)" {
		t.Fatalf("custom task StatusLabel = %q, want 已关闭(手动)", got)
	}

	// 改名迁移测试
	if err := reloaded.RenameTask(customTask.Name, `\RenamedBackup`); err != nil {
		t.Fatalf("RenameTask failed: %v", err)
	}
	renamedTask := Task{Name: `\RenamedBackup`, Location: `\`}
	if reloaded.IsAuditEnabled(renamedTask) {
		t.Fatalf("renamed task should inherit disabled override")
	}
	// 原名称覆盖已清理，回退到默认 true
	if !reloaded.IsAuditEnabled(customTask) {
		t.Fatalf("old task name override should have been removed after rename")
	}

	// 删除清理测试
	if err := reloaded.DeleteTask(renamedTask.Name); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}
	if !reloaded.IsAuditEnabled(renamedTask) {
		t.Fatalf("deleted override should revert to default true")
	}
}
