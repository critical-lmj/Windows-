package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWevtutilXMLAndPairEvents(t *testing.T) {
	xmlData := `
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>100</EventID>
    <TimeCreated SystemTime="2026-11-23T10:00:00.000000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\taxt php log</Data>
    <Data Name="InstanceId">{11111111-2222-3333-4444-555555555555}</Data>
  </EventData>
</Event>
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>201</EventID>
    <TimeCreated SystemTime="2026-11-23T10:00:05.500000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\taxt php log</Data>
    <Data Name="ActionName">php.exe</Data>
    <Data Name="ResultCode">0</Data>
    <Data Name="InstanceId">{11111111-2222-3333-4444-555555555555}</Data>
  </EventData>
</Event>
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>102</EventID>
    <TimeCreated SystemTime="2026-11-23T10:00:05.500000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\taxt php log</Data>
    <Data Name="InstanceId">{11111111-2222-3333-4444-555555555555}</Data>
  </EventData>
</Event>
`

	events, err := parseWevtutilXML(xmlData)
	if err != nil {
		t.Fatalf("parseWevtutilXML failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	records := pairExecutionEvents(events, `\taxt php log`)
	if len(records) != 1 {
		t.Fatalf("expected 1 paired record, got %d", len(records))
	}
	r := records[0]
	if r.TaskName != `\taxt php log` {
		t.Fatalf("TaskName = %q, want \\taxt php log", r.TaskName)
	}
	if r.Status != "成功" {
		t.Fatalf("Status = %q, want 成功", r.Status)
	}
	if r.ExitCode != "0" {
		t.Fatalf("ExitCode = %q, want 0", r.ExitCode)
	}
	if r.Action != "php.exe" {
		t.Fatalf("Action = %q, want php.exe", r.Action)
	}
}

func TestPairExecutionEventsFailedAndOngoing(t *testing.T) {
	xmlData := `
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>100</EventID>
    <TimeCreated SystemTime="2026-11-23T11:00:00.000000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\fail_task</Data>
    <Data Name="InstanceId">{FAIL-1111}</Data>
  </EventData>
</Event>
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>201</EventID>
    <TimeCreated SystemTime="2026-11-23T11:00:01.000000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\fail_task</Data>
    <Data Name="ResultCode">1</Data>
    <Data Name="InstanceId">{FAIL-1111}</Data>
  </EventData>
</Event>
<Event xmlns="http://schemas.microsoft.com/win/2004/08/events/event">
  <System>
    <Provider Name="Microsoft-Windows-TaskScheduler" />
    <EventID>100</EventID>
    <TimeCreated SystemTime="2026-11-23T12:00:00.000000000Z" />
  </System>
  <EventData>
    <Data Name="TaskName">\running_task</Data>
    <Data Name="InstanceId">{RUNNING-2222}</Data>
  </EventData>
</Event>
`

	events, err := parseWevtutilXML(xmlData)
	if err != nil {
		t.Fatalf("parseWevtutilXML failed: %v", err)
	}

	records := pairExecutionEvents(events, "")
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// {FAIL-1111} should have status "失败 (1)"
	failRec := records[0]
	if failRec.Status != "失败 (1)" {
		t.Fatalf("failRec.Status = %q, want 失败 (1)", failRec.Status)
	}

	// {RUNNING-2222} should have status "运行中"
	runRec := records[1]
	if runRec.Status != "运行中" || runRec.Duration != "运行中" {
		t.Fatalf("runRec.Status = %q, Duration = %q", runRec.Status, runRec.Duration)
	}
}

func TestParseChannelEnabledFromText(t *testing.T) {
	enabledText := `name: Microsoft-Windows-TaskScheduler/Operational
enabled: true
type: Operational`
	if !parseChannelEnabledFromText(enabledText) {
		t.Fatalf("expected true for enabled channel text")
	}

	disabledText := `name: Microsoft-Windows-TaskScheduler/Operational
enabled: false
type: Operational`
	if parseChannelEnabledFromText(disabledText) {
		t.Fatalf("expected false for disabled channel text")
	}
}

func TestExportTaskExecutionHistoryToCSV(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "history.csv")
	records := []TaskExecutionRecord{
		{
			TaskName:  `\test_task`,
			StartTime: "2026-11-24 10:00:00",
			EndTime:   "2026-11-24 10:00:05",
			Duration:  "5.00s",
			ExitCode:  "0",
			Status:    "成功",
			Action:    "cmd.exe",
		},
	}
	err := exportTaskHistoryToCSV(records, tmpFile)
	if err != nil {
		t.Fatalf("exportTaskHistoryToCSV failed: %v", err)
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
	if !strings.Contains(text, "任务名称") || !strings.Contains(text, `\test_task`) || !strings.Contains(text, "成功") {
		t.Fatalf("unexpected CSV content: %s", text)
	}
}

func TestNewHiddenWevtutilCmdSetsHideWindow(t *testing.T) {
	cmd := newHiddenWevtutilCmd("gl", "Microsoft-Windows-TaskScheduler/Operational")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("expected HideWindow=true on wevtutil command to prevent console popups, got %#v", cmd.SysProcAttr)
	}
}

func TestPairExecutionEventsRealWindowsTaskInstanceIdAndCorrelation(t *testing.T) {
	// 真实 Windows 事件日志中，100/102 使用 InstanceId，200/201 使用 TaskInstanceId，同时带有 System.Correlation.ActivityID
	xmlData := `
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'>
  <System>
    <EventID>102</EventID>
    <TimeCreated SystemTime='2026-11-24T06:34:00.2000000Z'/>
    <Correlation ActivityID='{6c0148b1-13a9-4197-85fa-854bc2fa9442}'/>
  </System>
  <EventData Name='TaskSuccessEvent'>
    <Data Name='TaskName'>\MyPhpTask</Data>
    <Data Name='InstanceId'>{6c0148b1-13a9-4197-85fa-854bc2fa9442}</Data>
  </EventData>
</Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'>
  <System>
    <EventID>201</EventID>
    <TimeCreated SystemTime='2026-11-24T06:34:00.1990000Z'/>
    <Correlation ActivityID='{6c0148b1-13a9-4197-85fa-854bc2fa9442}'/>
  </System>
  <EventData Name='ActionSuccess'>
    <Data Name='TaskName'>\MyPhpTask</Data>
    <Data Name='TaskInstanceId'>{6c0148b1-13a9-4197-85fa-854bc2fa9442}</Data>
    <Data Name='ActionName'>D:\MYOA\bin\php.exe</Data>
    <Data Name='ResultCode'>0</Data>
  </EventData>
</Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'>
  <System>
    <EventID>200</EventID>
    <TimeCreated SystemTime='2026-11-24T06:34:00.0330000Z'/>
    <Correlation ActivityID='{6c0148b1-13a9-4197-85fa-854bc2fa9442}'/>
  </System>
  <EventData Name='ActionStart'>
    <Data Name='TaskName'>\MyPhpTask</Data>
    <Data Name='ActionName'>D:\MYOA\bin\php.exe</Data>
    <Data Name='TaskInstanceId'>{6c0148b1-13a9-4197-85fa-854bc2fa9442}</Data>
  </EventData>
</Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'>
  <System>
    <EventID>100</EventID>
    <TimeCreated SystemTime='2026-11-24T06:34:00.0320000Z'/>
    <Correlation ActivityID='{6c0148b1-13a9-4197-85fa-854bc2fa9442}'/>
  </System>
  <EventData Name='TaskStartEvent'>
    <Data Name='TaskName'>\MyPhpTask</Data>
    <Data Name='InstanceId'>{6c0148b1-13a9-4197-85fa-854bc2fa9442}</Data>
  </EventData>
</Event>
`
	events, err := parseWevtutilXML(xmlData)
	if err != nil {
		t.Fatalf("parseWevtutilXML failed: %v", err)
	}
	records := pairExecutionEvents(events, `\MyPhpTask`)
	if len(records) != 1 {
		t.Fatalf("expected 4 events to merge into 1 record, got %d: %#v", len(records), records)
	}
	r := records[0]
	if r.Status != "成功" || r.ExitCode != "0" || r.Action != `D:\MYOA\bin\php.exe` {
		t.Fatalf("unexpected paired record fields: %#v", r)
	}
	if r.StartTime == "" || r.EndTime == "" || r.Duration != "168ms" {
		t.Fatalf("unexpected time/duration pairing: %#v", r)
	}
}
