package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTaskScriptPathUsesStartInForRelativePhpScript(t *testing.T) {
	task := Task{
		TaskToRun: `E:\software\php\php.exe -f "write_md5_log.php"`,
		StartIn:   `E:\work\scheduled_task\`,
	}

	got := taskScriptPath(task)
	want := `E:\work\scheduled_task\write_md5_log.php`
	if got != want {
		t.Fatalf("taskScriptPath() = %q, want %q", got, want)
	}
}

func TestLockUIThreadUsesConfiguredThreadLocker(t *testing.T) {
	original := uiThreadLocker
	defer func() { uiThreadLocker = original }()

	called := false
	uiThreadLocker = func() { called = true }
	lockUIThread()
	if !called {
		t.Fatal("lockUIThread() did not bind the Win32 UI goroutine to its OS thread")
	}
}

func TestTaskScriptPathKeepsAbsolutePhpScript(t *testing.T) {
	task := Task{
		TaskToRun: `E:\software\php\php.exe -f "E:\work\scheduled_task\write_md5_log.php"`,
		StartIn:   `E:\software\php`,
	}

	got := taskScriptPath(task)
	want := `E:\work\scheduled_task\write_md5_log.php`
	if got != want {
		t.Fatalf("taskScriptPath() = %q, want %q", got, want)
	}
}

func TestBuildCreateArgsOnce(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `once php`,
		Schedule:  "一次",
		StartDate: "2026-7-6",
		StartTime: "09:05",
		Program:   `D:\MYOA\bin\php.exe`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}
	assertContainsInOrder(t, args, []string{
		"/create", "/tn", `\once php`,
		"/sc", "ONCE", "/st", "09:05",
		"/ru", "SYSTEM", "/rl", "HIGHEST",
		"/sd", "2026/07/06",
	})
}

func TestBuildCreateArgsDaily(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `daily php`,
		Schedule:  "每天",
		StartDate: "2026/7/6",
		StartTime: "19:30",
		Program:   `D:\MYOA\bin\php.exe`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}
	assertContainsInOrder(t, args, []string{
		"/sc", "DAILY", "/st", "19:30",
		"/sd", "2026/07/06",
	})
}

func TestNormalizeTaskNamePreservesUserInput(t *testing.T) {
	got, err := normalizeTaskName("MyTask")
	if err != nil {
		t.Fatal(err)
	}
	if got != `\MyTask` {
		t.Fatalf("normalizeTaskName() = %q, want %q", got, `\MyTask`)
	}
}

func TestRunCommandWithTimeoutStopsHungProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "sleep")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	start := time.Now()
	out, err := runCommandWithTimeout(cmd, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("runCommandWithTimeout() expected timeout error, got nil output=%q", out)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("runCommandWithTimeout() waited too long: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("runCommandWithTimeout() error = %q, want timeout message", err.Error())
	}
}

func TestDecodePowerShellBase64Output(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-16"?><Task><Actions /></Task>`
	out := "  " + base64.StdEncoding.EncodeToString([]byte(want)) + "\r\n"
	got, err := decodePowerShellBase64Output(out)
	if err != nil {
		t.Fatalf("decodePowerShellBase64Output() returned error: %v", err)
	}
	if got != want {
		t.Fatalf("decodePowerShellBase64Output() = %q, want %q", got, want)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) > 0 && os.Args[len(os.Args)-1] == "sleep" {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
	os.Exit(2)
}

func TestParseTaskXMLConfigAllSupportedSchedules(t *testing.T) {
	tests := []struct {
		name     string
		trigger  string
		schedule string
	}{
		{"once", `<TimeTrigger><StartBoundary>2026-07-07T09:00:00</StartBoundary></TimeTrigger>`, "一次"},
		{"daily", `<CalendarTrigger><StartBoundary>2026-07-07T09:00:00</StartBoundary><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger>`, "每天"},
		{"weekly", `<CalendarTrigger><StartBoundary>2026-07-07T09:00:00</StartBoundary><ScheduleByWeek><WeeksInterval>1</WeeksInterval><DaysOfWeek><Monday/><Wednesday/></DaysOfWeek></ScheduleByWeek></CalendarTrigger>`, "每周"},
		{"monthly", `<CalendarTrigger><StartBoundary>2026-07-07T09:00:00</StartBoundary><ScheduleByMonth><DaysOfMonth><Day>15</Day></DaysOfMonth><Months><January/></Months></ScheduleByMonth></CalendarTrigger>`, "每月"},
		{"shutdown", `<EventTrigger><Subscription>&lt;QueryList&gt;&lt;Query Id="0" Path="System"&gt;&lt;Select Path="System"&gt;*[System[Provider[@Name='USER32'] and EventID=1074]]&lt;/Select&gt;&lt;/Query&gt;&lt;/QueryList&gt;</Subscription></EventTrigger>`, "关机时"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			xmlText := `<?xml version="1.0" encoding="UTF-8"?><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Description>保持名称</Description></RegistrationInfo><Triggers>` + test.trigger + `</Triggers><Actions><Exec><Command>D:\MYOA\bin\php.exe</Command><Arguments>D:\work\textphp\write_md5_log.php</Arguments><WorkingDirectory>D:\work\textphp</WorkingDirectory></Exec></Actions></Task>`
			config, err := parseTaskXMLConfig(`\MyTask`, xmlText)
			if err != nil {
				t.Fatalf("parseTaskXMLConfig() error: %v", err)
			}
			if config.Name != `\MyTask` || config.Schedule != test.schedule {
				t.Fatalf("unexpected config: %+v", config)
			}
			if config.Description != "保持名称" || config.StartIn != `D:\work\textphp` {
				t.Fatalf("metadata was not parsed: %+v", config)
			}
		})
	}
}

func TestParseTaskXMLConfigWithRepeatInterval(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Description>重复任务</Description></RegistrationInfo><Triggers><CalendarTrigger><StartBoundary>2026-07-08T09:00:00</StartBoundary><Repetition><Interval>PT1H</Interval><Duration>P1D</Duration></Repetition><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger></Triggers><Actions><Exec><Command>D:\MYOA\bin\php.exe</Command><Arguments>D:\work\textphp\write_md5_log.php</Arguments><WorkingDirectory>D:\work\textphp</WorkingDirectory></Exec></Actions></Task>`
	config, err := parseTaskXMLConfig(`\RepeatTask`, xmlText)
	if err != nil {
		t.Fatalf("parseTaskXMLConfig() error: %v", err)
	}
	if config.RepeatIntervalMinutes != 60 || config.RepeatDurationMinutes != 1440 {
		t.Fatalf("unexpected repeat settings: interval=%d duration=%d", config.RepeatIntervalMinutes, config.RepeatDurationMinutes)
	}
}

func TestParseTaskXMLConfigMonthlyLastDay(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{"flag", `<ScheduleByMonth><RunOnLastDayOfMonth>true</RunOnLastDayOfMonth></ScheduleByMonth>`},
		{"day-last", `<ScheduleByMonth><DaysOfMonth><Day>LAST</Day></DaysOfMonth></ScheduleByMonth>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			xmlText := `<?xml version="1.0" encoding="UTF-8"?><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Description>月底任务</Description></RegistrationInfo><Triggers><CalendarTrigger><StartBoundary>2026-07-08T09:00:00</StartBoundary>` + test.xml + `</CalendarTrigger></Triggers><Actions><Exec><Command>D:\MYOA\bin\php.exe</Command></Exec></Actions></Task>`
			config, err := parseTaskXMLConfig(`\MonthEndTask`, xmlText)
			if err != nil {
				t.Fatalf("parseTaskXMLConfig() error: %v", err)
			}
			if config.Schedule != "每月" || config.MonthDay != "LASTDAY" {
				t.Fatalf("unexpected monthly config: %+v", config)
			}
		})
	}
}

func TestBuildCreateArgsWeekly(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `text php`,
		Schedule:  "每周",
		StartDate: "2026-6-19",
		StartTime: "19:30",
		WeekDays:  []string{"MON", "WED"},
		Program:   `E:\software\php\php.exe`,
		Arguments: `-f "E:\work\scheduled_task\write_md5_log.php"`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}

	assertContainsInOrder(t, args, []string{
		"/create", "/tn", `\text php`,
		"/sc", "WEEKLY",
		"/st", "19:30",
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
		"/sd", "2026/06/19",
		"/d", "MON,WED",
	})
}

func TestBuildCreateArgsMonthly(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `monthly php`,
		Schedule:  "每月",
		StartDate: "2026/06/19",
		StartTime: "09:00",
		MonthDay:  "15",
		Program:   `E:\software\php\php.exe`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}

	assertContainsInOrder(t, args, []string{
		"/create", "/tn", `\monthly php`,
		"/sc", "MONTHLY",
		"/st", "09:00",
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
		"/sd", "2026/06/19",
		"/d", "15",
	})
}

func TestBuildCreateArgsMonthlyLastDayUsesModifier(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `monthly last php`,
		Schedule:  "每月",
		StartDate: "2026/07/08",
		StartTime: "09:00",
		MonthDay:  "LASTDAY",
		Program:   `E:\software\php\php.exe`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}
	assertContainsInOrder(t, args, []string{
		"/sc", "MONTHLY",
		"/mo", "LASTDAY",
		"/m", "*",
	})
	assertNotContains(t, args, "/d")
}

func TestBuildCreateArgsDailyWithRepeatInterval(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:                  `repeat php`,
		Schedule:              "每天",
		StartDate:             "2026/07/08",
		StartTime:             "09:00",
		Program:               `E:\software\php\php.exe`,
		RepeatIntervalMinutes: 60,
		RepeatDurationMinutes: 1440,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}
	assertContainsInOrder(t, args, []string{
		"/sc", "DAILY",
		"/ri", "60",
		"/du", "24:00",
	})
}

func TestBuildCreateArgsShutdownEvent(t *testing.T) {
	args, err := buildCreateArgs(TaskCreateConfig{
		Name:      `shutdown php`,
		Schedule:  "关机时",
		StartDate: "2026/06/19",
		StartTime: "09:00",
		Program:   `E:\software\php\php.exe`,
		Arguments: `-f "E:\work\scheduled_task\write_md5_log.php"`,
	})
	if err != nil {
		t.Fatalf("buildCreateArgs() returned error: %v", err)
	}

	assertContainsInOrder(t, args, []string{
		"/create", "/tn", `\shutdown php`,
		"/sc", "ONEVENT",
		"/ec", "System",
		"/mo", "*[System[Provider[@Name='USER32'] and EventID=1074]]",
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
	})
	assertNotContains(t, args, "/st")
	assertNotContains(t, args, "/sd")
}

func TestBuildCreateArgsWeeklyRequiresDay(t *testing.T) {
	_, err := buildCreateArgs(TaskCreateConfig{
		Name:      `weekly php`,
		Schedule:  "每周",
		StartTime: "09:00",
		Program:   `E:\software\php\php.exe`,
	})
	if err == nil {
		t.Fatal("buildCreateArgs() expected error for weekly task without weekday")
	}
}

func TestBuildCreateArgsRejectsInvalidInputs(t *testing.T) {
	base := TaskCreateConfig{Name: "test", Schedule: "每天", StartDate: "2026/07/06", StartTime: "09:00", Program: "cmd.exe"}

	invalidDate := base
	invalidDate.StartDate = "2026/13/40"
	if _, err := buildCreateArgs(invalidDate); err == nil {
		t.Fatal("expected invalid date error")
	}

	invalidTime := base
	invalidTime.StartTime = "25:00"
	if _, err := buildCreateArgs(invalidTime); err == nil {
		t.Fatal("expected invalid time error")
	}

	invalidSchedule := base
	invalidSchedule.Schedule = "每分钟"
	if _, err := buildCreateArgs(invalidSchedule); err == nil {
		t.Fatal("expected unsupported schedule error")
	}

	invalidMonthDay := base
	invalidMonthDay.Schedule = "每月"
	invalidMonthDay.MonthDay = "32"
	if _, err := buildCreateArgs(invalidMonthDay); err == nil {
		t.Fatal("expected invalid month day error")
	}
}

func TestShutdownScheduleIgnoresDateAndTime(t *testing.T) {
	_, err := buildCreateArgs(TaskCreateConfig{
		Name: "shutdown", Schedule: "关机时", StartDate: "not-a-date", StartTime: "not-a-time", Program: "cmd.exe",
	})
	if err != nil {
		t.Fatalf("shutdown schedule should ignore date and time: %v", err)
	}
}

func TestValidateSystemTaskPrivilegeRequiresElevation(t *testing.T) {
	if err := validateSystemTaskPrivilege(true, false); err == nil {
		t.Fatal("expected SYSTEM task creation to require elevation")
	}
	if err := validateSystemTaskPrivilege(true, true); err != nil {
		t.Fatalf("elevated SYSTEM task creation should be allowed: %v", err)
	}
	if err := validateSystemTaskPrivilege(false, false); err != nil {
		t.Fatalf("current-user task creation should not require elevation: %v", err)
	}
}

func TestValidateSystemPrincipalXML(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?><Task><Principals><Principal><UserId>SYSTEM</UserId><RunLevel>HighestAvailable</RunLevel></Principal></Principals></Task>`
	if err := validateSystemPrincipalXML(xmlText); err != nil {
		t.Fatalf("SYSTEM principal should be valid: %v", err)
	}
	userXML := `<?xml version="1.0" encoding="UTF-8"?><Task><Principals><Principal><UserId>DESKTOP\User</UserId><RunLevel>LeastPrivilege</RunLevel></Principal></Principals></Task>`
	if err := validateSystemPrincipalXML(userXML); err == nil {
		t.Fatal("non-SYSTEM principal should be rejected")
	}
}

func TestParseTaskCSVOutput(t *testing.T) {
	input := `"HostName","TaskName","Next Run Time","Status","Last Run Time","Last Result","Task To Run","Start In","Comment","Scheduled Task State"` + "\n" +
		`"SERVER","\text php","2026/6/20 0:43:43","Ready","2026/6/19 11:37:07","0","E:\software\php\php.exe -f ""E:\work\scheduled_task\write_md5_log.php""","E:\work\scheduled_task\","测试php写入日志","Enabled"`

	tasks := parseTaskCSVOutput(input)
	if len(tasks) != 1 {
		t.Fatalf("parseTaskCSVOutput() returned %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Name != `\text php` || task.ShortName != "text php" || task.Location != `\` {
		t.Fatalf("unexpected task identity: %#v", task)
	}
	if task.TaskToRun == "" || task.StartIn != `E:\work\scheduled_task\` || !task.Enabled {
		t.Fatalf("unexpected task fields: %#v", task)
	}
}

func TestParseTaskListOutput(t *testing.T) {
	input := `
任务名:                             \text php
下次运行时间:                       2026/6/20 0:43:43
状态:                               准备就绪
上次运行时间:                       2026/6/19 11:37:07
上次结果:                           0
要运行的任务:                       E:\software\php\php.exe -f "E:\work\scheduled_task\write_md5_log.php"
起始于:                             E:\work\scheduled_task\
注释:                               测试php写入日志
计划的任务状态:                     已启用
`

	tasks := parseTaskListOutput(input)
	if len(tasks) != 1 {
		t.Fatalf("parseTaskListOutput() returned %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Name != `\text php` || task.TaskToRun == "" || task.StartIn != `E:\work\scheduled_task\` {
		t.Fatalf("unexpected parsed task: %#v", task)
	}
}

func TestParsePowerShellTaskOutput(t *testing.T) {
	line := strings.Join([]string{
		encodeTestField(`\text php`),
		encodeTestField("Ready"),
		encodeTestField("2026/6/20 0:43:43"),
		encodeTestField("2026/6/19 11:37:07"),
		encodeTestField("0"),
		encodeTestField("每天"),
		encodeTestField(`E:\software\php\php.exe -f "E:\work\scheduled_task\write_md5_log.php"`),
		encodeTestField(`E:\work\scheduled_task\`),
		encodeTestField("SYSTEM"),
		encodeTestField("测试php写入日志"),
		encodeTestField("true"),
	}, "\t")

	tasks := parsePowerShellTaskOutput(line)
	if len(tasks) != 1 {
		t.Fatalf("parsePowerShellTaskOutput() returned %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Name != `\text php` || task.StartIn != `E:\work\scheduled_task\` || !task.Enabled {
		t.Fatalf("unexpected parsed task: %#v", task)
	}
}

func TestParsePowerShellTaskOutputNormalizesNeverRunTime(t *testing.T) {
	line := strings.Join([]string{
		encodeTestField(`\month end`),
		encodeTestField("Ready"),
		encodeTestField("2026/7/31 9:00:00"),
		encodeTestField("1999/11/30 0:00:00"),
		encodeTestField("267011"),
		encodeTestField("每月"),
		encodeTestField(`cmd.exe /c exit 0`),
		encodeTestField(""),
		encodeTestField("SYSTEM"),
		encodeTestField(""),
		encodeTestField("true"),
	}, "\t")

	tasks := parsePowerShellTaskOutput(line)
	if len(tasks) != 1 {
		t.Fatalf("parsePowerShellTaskOutput() returned %d tasks, want 1", len(tasks))
	}
	if tasks[0].LastRunTime != "N/A" {
		t.Fatalf("LastRunTime = %q, want N/A", tasks[0].LastRunTime)
	}
}

func TestMergeTasksByNameCombinesMultipleTriggers(t *testing.T) {
	tasks := []Task{
		{Name: `\Folder\Task`, ScheduleType: "At logon time", TaskToRun: "cmd.exe"},
		{Name: `\Folder\Task`, ScheduleType: "Daily", TaskToRun: "cmd.exe"},
		{Name: `\Folder\Other`, ScheduleType: "Weekly"},
	}

	merged := mergeTasksByName(tasks)
	if len(merged) != 2 {
		t.Fatalf("expected 2 tasks after merge, got %d", len(merged))
	}
	if merged[0].ScheduleType != "At logon time, Daily" {
		t.Fatalf("unexpected merged schedule: %q", merged[0].ScheduleType)
	}
}

func TestDecodeANSIPreservesUTF8(t *testing.T) {
	const text = "任务描述：每天运行"
	if got := decodeANSI([]byte(text)); got != text {
		t.Fatalf("UTF-8 text was changed: got %q", got)
	}
}

func TestParseTaskListOutputAcceptsChineseModeHeader(t *testing.T) {
	out := "任务名: \\Folder\\DisabledTask\r\n模式: 已禁用\r\n要运行的任务: cmd.exe\r\n\r\n"
	tasks := parseTaskListOutput(out)
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	if tasks[0].Status != "已禁用" || tasks[0].Enabled {
		t.Fatalf("unexpected status parsing: %+v", tasks[0])
	}
}

func assertContainsInOrder(t *testing.T, values, subsequence []string) {
	t.Helper()
	pos := 0
	for _, want := range subsequence {
		found := false
		for pos < len(values) {
			if reflect.DeepEqual(values[pos], want) {
				found = true
				pos++
				break
			}
			pos++
		}
		if !found {
			t.Fatalf("args %q do not contain %q in order", values, subsequence)
		}
	}
}

func encodeTestField(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func assertNotContains(t *testing.T, values []string, unexpected string) {
	t.Helper()
	for _, value := range values {
		if value == unexpected {
			t.Fatalf("args %q should not contain %q", values, unexpected)
		}
	}
}
