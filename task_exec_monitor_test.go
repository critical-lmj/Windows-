package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyExecLogPath(t *testing.T) {
	tm, err := time.Parse("2006-01-02", "2026-11-25")
	if err != nil {
		t.Fatalf("parse time failed: %v", err)
	}
	got := dailyExecLogPath(`D:\logs`, tm)
	want := filepath.Join(`D:\logs`, "exec_2026-11-25.jsonl")
	if got != want {
		t.Fatalf("dailyExecLogPath() = %q, want %q", got, want)
	}
}

func TestExecLogDailySplitCrossDayReadAndInstanceDedup(t *testing.T) {
	tmpDir := t.TempDir()

	day1 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 11, 25, 9, 0, 0, 0, time.Local)

	// Day 1: 一条系统级任务执行完成
	if err := appendExecRecordToDir(tmpDir, TaskExecutionRecord{
		InstanceID: `\microsoft\windows\cleanup|2026-11-24 10:00:00`,
		TaskName:   `\Microsoft\Windows\Cleanup`,
		Action:     `cleanmgr.exe`,
		StartTime:  "2026-11-24 10:00:00",
		EndTime:    "2026-11-24 10:00:05",
		Duration:   "5.0s",
		ExitCode:   "0",
		Status:     "成功",
	}, day1); err != nil {
		t.Fatalf("append day1 record failed: %v", err)
	}

	// Day 2: 自定义任务先落盘“运行中”，后落盘同 InstanceID 的“成功”
	instID := `\mybackup|2026-11-25 09:00:00`
	if err := appendExecRecordToDir(tmpDir, TaskExecutionRecord{
		InstanceID: instID,
		TaskName:   `\MyBackup`,
		Action:     `php.exe backup.php`,
		StartTime:  "2026-11-25 09:00:00",
		Duration:   "运行中",
		Status:     "运行中",
	}, day2); err != nil {
		t.Fatalf("append day2 running record failed: %v", err)
	}
	if err := appendExecRecordToDir(tmpDir, TaskExecutionRecord{
		InstanceID: instID,
		TaskName:   `\MyBackup`,
		Action:     `php.exe backup.php`,
		StartTime:  "2026-11-25 09:00:00",
		EndTime:    "2026-11-25 09:00:12",
		Duration:   "12.0s",
		ExitCode:   "0",
		Status:     "成功",
	}, day2.Add(12*time.Second)); err != nil {
		t.Fatalf("append day2 finished record failed: %v", err)
	}

	// 验证磁盘生成了按天拆分的两个文件
	if _, err := os.Stat(filepath.Join(tmpDir, "exec_2026-11-24.jsonl")); err != nil {
		t.Fatalf("expected exec_2026-11-24.jsonl to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "exec_2026-11-25.jsonl")); err != nil {
		t.Fatalf("expected exec_2026-11-25.jsonl to exist: %v", err)
	}

	// 全量跨天读取，应合并去重为 2 条（Day 2 完成态排前，Day 1 排后）
	allRecs, err := readRecentExecRecords(tmpDir, "", -1)
	if err != nil {
		t.Fatalf("readRecentExecRecords failed: %v", err)
	}
	if len(allRecs) != 2 {
		t.Fatalf("expected 2 deduplicated records across days, got %d: %#v", len(allRecs), allRecs)
	}
	if allRecs[0].TaskName != `\MyBackup` || allRecs[0].Status != "成功" || allRecs[0].Duration != "12.0s" {
		t.Fatalf("unexpected newest record[0]: %#v", allRecs[0])
	}
	if allRecs[1].TaskName != `\Microsoft\Windows\Cleanup` || allRecs[1].Status != "成功" {
		t.Fatalf("unexpected older record[1]: %#v", allRecs[1])
	}

	// 按任务名过滤
	filtered, err := readRecentExecRecords(tmpDir, `\MyBackup`, 10)
	if err != nil {
		t.Fatalf("readRecentExecRecords(filtered) failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].TaskName != `\MyBackup` {
		t.Fatalf("unexpected filtered records: %#v", filtered)
	}
}

func TestTaskExecMonitorDiffAndPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Date(2026, 11, 25, 11, 0, 0, 0, time.Local)

	// 模拟 4 轮轮询快照
	polls := [][]Task{
		// 第 1 轮（冷启动建立基线）：老历史不刷屏
		{
			{Name: `\Backup`, Status: "Ready", LastRunTime: "2026/11/24 08:00:00", LastResult: "0", TaskToRun: "backup.bat"},
			{Name: `\Microsoft\Windows\SysJob`, Status: "Ready", LastRunTime: "2026/11/24 07:00:00", LastResult: "0", TaskToRun: "sys.exe"},
		},
		// 第 2 轮：\Backup 开始运行；系统任务 \Microsoft\Windows\SysJob 在间隔内直接跑完并失败(1)
		{
			{Name: `\Backup`, Status: "Running", LastRunTime: "2026/11/25 11:00:10", LastResult: "267009", TaskToRun: "backup.bat"},
			{Name: `\Microsoft\Windows\SysJob`, Status: "Ready", LastRunTime: "2026/11/25 11:00:08", LastResult: "1", TaskToRun: "sys.exe"},
		},
		// 第 3 轮：\Backup 运行结束成功
		{
			{Name: `\Backup`, Status: "Ready", LastRunTime: "2026/11/25 11:00:10", LastResult: "0", TaskToRun: "backup.bat"},
			{Name: `\Microsoft\Windows\SysJob`, Status: "Ready", LastRunTime: "2026/11/25 11:00:08", LastResult: "1", TaskToRun: "sys.exe"},
		},
		// 第 4 轮：无任何变化，应返回 0 条新记录
		{
			{Name: `\Backup`, Status: "Ready", LastRunTime: "2026/11/25 11:00:10", LastResult: "0", TaskToRun: "backup.bat"},
			{Name: `\Microsoft\Windows\SysJob`, Status: "Ready", LastRunTime: "2026/11/25 11:00:08", LastResult: "1", TaskToRun: "sys.exe"},
		},
	}

	pollIdx := 0
	mon := newTaskExecMonitor(tmpDir, time.Second, func() ([]Task, error) {
		res := polls[pollIdx]
		return res, nil
	})
	mon.nowFn = func() time.Time { return now }

	// 第 1 轮：建立基线，0 条记录
	n, err := mon.PollOnce()
	if err != nil || n != 0 {
		t.Fatalf("poll 1: got (%d, %v), want (0, nil)", n, err)
	}

	// 第 2 轮：+10s，\Backup 运行中 + \Microsoft\Windows\SysJob 短周期完成失败 -> 2 条
	pollIdx = 1
	now = now.Add(10 * time.Second)
	n, err = mon.PollOnce()
	if err != nil || n != 2 {
		t.Fatalf("poll 2: got (%d, %v), want (2, nil)", n, err)
	}

	// 第 3 轮：+15s，\Backup 运行结束 -> 1 条完成记录
	pollIdx = 2
	now = now.Add(15 * time.Second)
	n, err = mon.PollOnce()
	if err != nil || n != 1 {
		t.Fatalf("poll 3: got (%d, %v), want (1, nil)", n, err)
	}

	// 模拟进程重启后重建 monitor，从 monitor_state.json 恢复基线，第 4 轮无变化应为 0 条
	pollIdx = 3
	now = now.Add(10 * time.Second)
	restarted := newTaskExecMonitor(tmpDir, time.Second, func() ([]Task, error) {
		return polls[pollIdx], nil
	})
	restarted.nowFn = func() time.Time { return now }
	n, err = restarted.PollOnce()
	if err != nil || n != 0 {
		t.Fatalf("poll 4 after restart: got (%d, %v), want (0, nil)", n, err)
	}

	// 验证落盘去重读取结果：共 2 条（\Backup 成功耗时 15.0s，\Microsoft\Windows\SysJob 失败 (1)）
	recs, err := readRecentExecRecords(tmpDir, "", 10)
	if err != nil {
		t.Fatalf("readRecentExecRecords failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 final records, got %d: %#v", len(recs), recs)
	}
	if recs[0].TaskName != `\Backup` || recs[0].Status != "成功" || recs[0].Duration != "15.0s" {
		t.Fatalf("unexpected record[0] (\\Backup): %#v", recs[0])
	}
	if recs[1].TaskName != `\Microsoft\Windows\SysJob` || !strings.HasPrefix(recs[1].Status, "失败") || recs[1].ExitCode != "1" {
		t.Fatalf("unexpected record[1] (\\Microsoft\\Windows\\SysJob): %#v", recs[1])
	}
}
