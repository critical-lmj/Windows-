package main

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	unicodeutf16 "unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/lxn/win"
)

// Task 是从 Windows 任务计划程序读取后在界面中展示的统一模型。
type Task struct {
	Name         string
	ShortName    string
	Location     string
	Status       string
	NextRunTime  string
	LastRunTime  string
	LastResult   string
	ScheduleType string
	TaskToRun    string
	StartIn      string
	Author       string
	Comment      string
	Enabled      bool
}

// TaskCreateConfig 收集“新建任务”窗口中的输入，再转换为 schtasks.exe 参数。
type TaskCreateConfig struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Schedule              string   `json:"schedule"`
	StartDate             string   `json:"startDate"`
	StartTime             string   `json:"startTime"`
	WeekDays              []string `json:"weekDays"`
	MonthDay              string   `json:"monthDay"`
	RepeatIntervalMinutes int      `json:"repeatIntervalMinutes"`
	RepeatDurationMinutes int      `json:"repeatDurationMinutes"`
	Program               string   `json:"program"`
	Arguments             string   `json:"arguments"`
	StartIn               string   `json:"startIn"`
}

type saveTaskRequest struct {
	Config        TaskCreateConfig `json:"config"`
	OriginalName  string           `json:"originalName"`
	SystemAccount bool             `json:"systemAccount"`
}

type schedulerTaskXML struct {
	RegistrationInfo struct {
		Description string `xml:"Description"`
	} `xml:"RegistrationInfo"`
	Principals struct {
		Principal []schedulerXMLPrincipal `xml:"Principal"`
	} `xml:"Principals"`
	Triggers schedulerXMLTriggers `xml:"Triggers"`
	Actions  schedulerXMLActions  `xml:"Actions"`
}

type schedulerXMLPrincipal struct {
	UserID    string `xml:"UserId"`
	LogonType string `xml:"LogonType"`
	RunLevel  string `xml:"RunLevel"`
}

type schedulerXMLTriggers struct {
	Time         []schedulerTimeTrigger     `xml:"TimeTrigger"`
	Calendar     []schedulerCalendarTrigger `xml:"CalendarTrigger"`
	Event        []schedulerEventTrigger    `xml:"EventTrigger"`
	Boot         []struct{}                 `xml:"BootTrigger"`
	Logon        []struct{}                 `xml:"LogonTrigger"`
	Idle         []struct{}                 `xml:"IdleTrigger"`
	Registration []struct{}                 `xml:"RegistrationTrigger"`
	Session      []struct{}                 `xml:"SessionStateChangeTrigger"`
}

type schedulerTimeTrigger struct {
	StartBoundary string                  `xml:"StartBoundary"`
	Repetition    *schedulerXMLRepetition `xml:"Repetition"`
}

type schedulerCalendarTrigger struct {
	StartBoundary   string                  `xml:"StartBoundary"`
	Repetition      *schedulerXMLRepetition `xml:"Repetition"`
	ScheduleByDay   *schedulerDaySchedule   `xml:"ScheduleByDay"`
	ScheduleByWeek  *schedulerWeekSchedule  `xml:"ScheduleByWeek"`
	ScheduleByMonth *schedulerMonthSchedule `xml:"ScheduleByMonth"`
}

type schedulerXMLRepetition struct {
	Interval string `xml:"Interval"`
	Duration string `xml:"Duration"`
}

type schedulerDaySchedule struct {
	DaysInterval int `xml:"DaysInterval"`
}

type schedulerWeekSchedule struct {
	WeeksInterval int                 `xml:"WeeksInterval"`
	DaysOfWeek    schedulerDaysOfWeek `xml:"DaysOfWeek"`
}

type schedulerDaysOfWeek struct {
	Sunday    *struct{} `xml:"Sunday"`
	Monday    *struct{} `xml:"Monday"`
	Tuesday   *struct{} `xml:"Tuesday"`
	Wednesday *struct{} `xml:"Wednesday"`
	Thursday  *struct{} `xml:"Thursday"`
	Friday    *struct{} `xml:"Friday"`
	Saturday  *struct{} `xml:"Saturday"`
}

type schedulerMonthSchedule struct {
	DaysOfMonth struct {
		Days []string `xml:"Day"`
	} `xml:"DaysOfMonth"`
	RunOnLastDayOfMonth bool `xml:"RunOnLastDayOfMonth"`
}

type schedulerEventTrigger struct {
	Subscription string    `xml:"Subscription"`
	Repetition   *struct{} `xml:"Repetition"`
}

type schedulerXMLActions struct {
	Exec        []schedulerExecAction `xml:"Exec"`
	ComHandler  []struct{}            `xml:"ComHandler"`
	SendEmail   []struct{}            `xml:"SendEmail"`
	ShowMessage []struct{}            `xml:"ShowMessage"`
}

type schedulerExecAction struct {
	Command          string `xml:"Command"`
	Arguments        string `xml:"Arguments"`
	WorkingDirectory string `xml:"WorkingDirectory"`
}

var (
	// Win32 窗口消息队列属于创建窗口的 OS 线程；UI 协程不能在阻塞外部命令后迁移到其他线程。
	uiThreadLocker = runtime.LockOSThread

	// 主窗口和主界面控件句柄。Win32 消息回调会通过这些 HWND 读写控件。
	hInst               win.HINSTANCE
	mainWnd             win.HWND
	searchEdit          win.HWND
	filterFieldCombo    win.HWND
	statusCombo         win.HWND
	listView            win.HWND
	detailEdit          win.HWND
	statLabel           win.HWND
	headerBand          win.HWND
	titleLabel          win.HWND
	subtitleLabel       win.HWND
	permissionLabel     win.HWND
	searchLabel         win.HWND
	searchScopeLabel    win.HWND
	actionLabel         win.HWND
	detailGroup         win.HWND
	refreshButton       win.HWND
	createButtonHwnd    win.HWND
	importButton        win.HWND
	exportButton        win.HWND
	backupButton        win.HWND
	editButton          win.HWND
	runButton           win.HWND
	endButton           win.HWND
	enableButton        win.HWND
	disableButton       win.HWND
	deleteButton        win.HWND
	uiFont              win.HFONT
	titleFont           win.HFONT
	sectionFont         win.HFONT
	pageBrush           win.HBRUSH
	contentBrush        win.HBRUSH
	headerBrush         win.HBRUSH
	borderBrush         win.HBRUSH
	pressedBrush        win.HBRUSH
	primaryBrush        win.HBRUSH
	primaryPressedBrush win.HBRUSH
	customBrushes       []win.HBRUSH
	allTasks            []Task
	viewTasks           []Task
	loadingTasks        bool
	operationBusy       bool
	archiveOperation    bool
	loadingStartedAt    time.Time
	operationStartedAt  time.Time
	lastWatchdogLog     time.Time
	lastLoadError       string
	pendingTasks        []Task
	pendingEditConfig   TaskCreateConfig
	pendingEditOriginal string
	lastEditError       string
	lastActionTitle     string
	lastActionMessage   string
	lastWizardTitle     string
	lastWizardMessage   string
	pendingImportPlan   []TaskImportPlanItem
	pendingTransfer     []TaskTransferResult
	pendingArchiveError string
	pendingArchivePath  string
	wizardSaving        bool
	wizardInitialConfig *TaskCreateConfig
	wizardOriginalName  string
)

const (
	ownerDrawButton = 4 // ODT_BUTTON，旧版 win 绑定未导出该常量。

	// 主窗口控件 ID。WM_COMMAND 事件通过这些 ID 区分按钮、输入框和下拉框。
	idSearch          = 1001
	idFilterField     = 1002
	idStatus          = 1003
	idRefresh         = 1004
	idCreate          = 1005
	idRun             = 1006
	idEnd             = 1007
	idEnable          = 1008
	idDisable         = 1009
	idDelete          = 1010
	idList            = 1011
	idEdit            = 1012
	idImport          = 1013
	idExport          = 1014
	idBackup          = 1015
	msgEditLoaded     = win.WM_APP + 4
	msgEditFailed     = win.WM_APP + 5
	msgActionDone     = win.WM_APP + 6
	msgActionFailed   = win.WM_APP + 7
	msgWizardSaved    = win.WM_APP + 8
	msgWizardSaveFail = win.WM_APP + 9
	msgArchiveDone    = win.WM_APP + 10
	msgImportReady    = win.WM_APP + 11
	msgImportDone     = win.WM_APP + 12
	msgCreateTask     = win.WM_APP + 1
	msgTasksLoaded    = win.WM_APP + 2
	msgTasksFailed    = win.WM_APP + 3
	idWizardName      = 3001
	idWizardDesc      = 3002
	idWizardSched     = 3003
	idWizardTime      = 3004
	idWizardProg      = 3005
	idWizardArgs      = 3006
	idWizardStart     = 3007
	idWizardBrowse    = 3008
	idWizardCreate    = 3009
	idWizardCancel    = 3010
	idWizardBrowseDir = 3011
	idWizardDate      = 3012
	idWizardMonthDay  = 3013
	idWizardRepeat    = 3014
	idWizardRepeatInt = 3015
	idWizardRepeatDur = 3016
	idWizardWeekBase  = 3020

	externalCommandTimeout = 30 * time.Second
	metadataCommandTimeout = 8 * time.Second
	idWatchdogTimer        = 9001
	watchdogIntervalMs     = 1000
	watchdogStaleAfter     = 45 * time.Second
)

var (
	// 新建任务弹窗控件句柄。
	wizardWnd          win.HWND
	wizardName         win.HWND
	wizardDesc         win.HWND
	wizardSched        win.HWND
	wizardDateLabel    win.HWND
	wizardDate         win.HWND
	wizardTimeLabel    win.HWND
	wizardTime         win.HWND
	wizardWeekLabel    win.HWND
	wizardWeekChecks   []win.HWND
	wizardMonthLabel   win.HWND
	wizardMonthDay     win.HWND
	wizardRepeatCheck  win.HWND
	wizardRepeatInt    win.HWND
	wizardRepeatDur    win.HWND
	wizardProgram      win.HWND
	wizardArgs         win.HWND
	wizardStartIn      win.HWND
	wizardSubmitButton win.HWND
)

func main() {
	lockUIThread()
	if len(os.Args) > 1 && os.Args[1] == "--save-task" {
		if err := runSaveTaskCommand(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}
	// 调试入口：不启动 GUI，直接验证创建、查询、删除任务链路。
	if len(os.Args) > 1 && os.Args[1] == "--self-test" {
		if err := selfTestScheduler(); err != nil {
			fmt.Println("SELF TEST FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("SELF TEST OK")
		return
	}
	// 调试入口：输出解析后的任务，用于排查 schtasks 输出和字段解析。
	if len(os.Args) > 1 && os.Args[1] == "--dump-tasks" {
		tasks, err := queryTasks()
		if err != nil {
			fmt.Println("DUMP FAILED:", err)
			os.Exit(1)
		}
		fmt.Printf("TASK COUNT: %d\n", len(tasks))
		for i, task := range tasks {
			if i >= 20 {
				break
			}
			fmt.Printf("%02d name=%q short=%q location=%q status=%q comment=%q command=%q\n", i+1, task.Name, task.ShortName, task.Location, task.Status, task.Comment, task.TaskToRun)
		}
		return
	}
	// 调试入口：绕过 schtasks，直接用任务计划程序 COM 接口读取任务。
	if len(os.Args) > 1 && os.Args[1] == "--dump-tasks-com" {
		tasks, err := queryTasksViaPowerShell()
		if err != nil {
			fmt.Println("COM DUMP FAILED:", err)
			os.Exit(1)
		}
		fmt.Printf("COM TASK COUNT: %d\n", len(tasks))
		for i, task := range tasks {
			if i >= 20 {
				break
			}
			fmt.Printf("%02d name=%q short=%q location=%q status=%q comment=%q command=%q\n", i+1, task.Name, task.ShortName, task.Location, task.Status, task.Comment, task.TaskToRun)
		}
		return
	}
	// 正式窗口程序尽量以管理员权限运行，否则部分系统任务会拒绝读取或管理。
	// 自动化界面检查可通过环境变量跳过 UAC；正常启动仍会请求管理员权限。
	elevatedAtStartup := isElevated()
	debugLog("startup elevated=%v skipElevation=%q", elevatedAtStartup, os.Getenv("TSM_SKIP_ELEVATION"))
	if !elevatedAtStartup && os.Getenv("TSM_SKIP_ELEVATION") != "1" {
		if relaunchElevated() {
			return
		}
		message(0, "权限不足", "本工具需要管理员权限运行，才能创建 NT AUTHORITY\\SYSTEM / 最高权限任务。\n\n请右键选择“以管理员身份运行”。")
		return
	}

	hInst = win.GetModuleHandle(nil)
	initCommonControls()
	initFonts()
	initBrushes()
	className := utf16("TaskSchedulerManagerWindow")
	title := utf16("Windows 定时任务管理器")

	var wc win.WNDCLASSEX
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wndProc)
	wc.HInstance = hInst
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW))
	wc.HIcon = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
	wc.HIconSm = wc.HIcon
	wc.HIcon = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
	wc.HIconSm = wc.HIcon
	wc.HbrBackground = pageBrush
	wc.LpszClassName = className

	if win.RegisterClassEx(&wc) == 0 {
		message(0, "启动失败", "注册窗口类失败。")
		return
	}

	mainWnd = win.CreateWindowEx(
		0,
		className,
		title,
		win.WS_OVERLAPPEDWINDOW|win.WS_CLIPCHILDREN,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
		1320,
		780,
		0,
		0,
		hInst,
		nil,
	)
	if mainWnd == 0 {
		message(0, "启动失败", "创建主窗口失败。")
		return
	}

	win.ShowWindow(mainWnd, win.SW_SHOW)
	win.UpdateWindow(mainWnd)
	refreshTasksAsync()

	var msg win.MSG
	for win.GetMessage(&msg, 0, 0, 0) > 0 {
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
}

// lockUIThread 将 GUI 主协程固定在同一个 OS 线程上，保证 GetMessage/DispatchMessage
// 始终操作创建主窗口的同一条 Win32 消息队列。
func lockUIThread() {
	uiThreadLocker()
}

// wndProc 是主窗口的 Win32 消息处理函数，负责分发创建、调整大小、按钮点击和异步加载结果。
func wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_CREATE:
		createControls(hwnd)
		win.SetTimer(hwnd, idWatchdogTimer, watchdogIntervalMs, 0)
		return 0
	case win.WM_SIZE:
		layoutControls(hwnd)
		return 0
	case win.WM_GETMINMAXINFO:
		if lParam != 0 {
			mmi := (*win.MINMAXINFO)(unsafe.Pointer(lParam))
			mmi.PtMinTrackSize.X = 1080
			mmi.PtMinTrackSize.Y = 650
		}
		return 0
	case win.WM_COMMAND:
		handleCommand(win.LOWORD(uint32(wParam)), win.HIWORD(uint32(wParam)))
		return 0
	case win.WM_DRAWITEM:
		if drawOwnerButton(lParam) {
			return 1
		}
		return 0
	case win.WM_CTLCOLORSTATIC:
		return controlColor(wParam, lParam, true)
	case win.WM_CTLCOLOREDIT:
		return controlColor(wParam, lParam, false)
	case win.WM_NOTIFY:
		return handleNotify(lParam)
	case win.WM_TIMER:
		if wParam == idWatchdogTimer {
			handleWatchdogTick()
			return 0
		}
		return 0
	case msgCreateTask:
		showTaskWizard()
		return 0
	case msgTasksLoaded:
		debugLog("UI received msgTasksLoaded: %d tasks", len(pendingTasks))
		loadingTasks = false
		loadingStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		allTasks = pendingTasks
		pendingTasks = nil
		applyFilter()
		return 0
	case msgTasksFailed:
		debugLog("UI received msgTasksFailed: %s", lastLoadError)
		loadingTasks = false
		loadingStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		updateActionButtons(nil)
		setWindowText(statLabel, "读取失败")
		message(mainWnd, "读取失败", lastLoadError)
		return 0
	case msgEditLoaded:
		debugLog("UI received msgEditLoaded: %s", pendingEditOriginal)
		operationBusy = false
		operationStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		wizardInitialConfig = &pendingEditConfig
		wizardOriginalName = pendingEditOriginal
		showTaskWizard()
		updateTaskDetailFromSelection()
		return 0
	case msgEditFailed:
		debugLog("UI received msgEditFailed: %s", lastEditError)
		operationBusy = false
		operationStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		updateTaskDetailFromSelection()
		message(mainWnd, "无法修改", lastEditError)
		return 0
	case msgActionDone:
		debugLog("UI received msgActionDone: %s", lastActionTitle)
		operationBusy = false
		operationStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		message(mainWnd, lastActionTitle, lastActionMessage)
		refreshTasksAsync()
		return 0
	case msgActionFailed:
		debugLog("UI received msgActionFailed: %s", lastActionMessage)
		operationBusy = false
		operationStartedAt = time.Time{}
		setMainToolbarEnabled(true)
		updateTaskDetailFromSelection()
		message(mainWnd, lastActionTitle, lastActionMessage)
		return 0
	case msgWizardSaved:
		debugLog("UI received msgWizardSaved: %s", lastWizardTitle)
		operationBusy = false
		operationStartedAt = time.Time{}
		wizardSaving = false
		setMainToolbarEnabled(true)
		message(wizardWnd, lastWizardTitle, lastWizardMessage)
		win.DestroyWindow(wizardWnd)
		refreshTasksAsync()
		return 0
	case msgWizardSaveFail:
		debugLog("UI received msgWizardSaveFail: %s", lastWizardMessage)
		operationBusy = false
		operationStartedAt = time.Time{}
		wizardSaving = false
		setMainToolbarEnabled(true)
		if wizardSubmitButton != 0 {
			win.EnableWindow(wizardSubmitButton, true)
		}
		message(wizardWnd, lastWizardTitle, lastWizardMessage)
		updateTaskDetailFromSelection()
		return 0
	case msgArchiveDone:
		debugLog("UI received msgArchiveDone error=%q results=%d", pendingArchiveError, len(pendingTransfer))
		if pendingArchiveError != "" {
			finishArchiveOperation("导出失败", pendingArchiveError+"\r\n\r\n"+archiveResultSummary(pendingTransfer, ""), archiveCompletionRefresh())
		} else {
			finishArchiveOperation("导出完成", archiveResultSummary(pendingTransfer, pendingArchivePath), archiveCompletionRefresh())
		}
		return 0
	case msgImportReady:
		if pendingArchiveError != "" {
			finishArchiveOperation("导入失败", pendingArchiveError, false)
			return 0
		}
		if !confirmImportPlan(pendingImportPlan) {
			finishArchiveOperation("已取消导入", "未修改任何任务。", false)
			return 0
		}
		startTaskImportAsync(pendingImportPlan)
		return 0
	case msgImportDone:
		finishArchiveOperation("导入完成", archiveResultSummary(pendingTransfer, ""), true)
		return 0
	case win.WM_DESTROY:
		win.KillTimer(hwnd, idWatchdogTimer)
		if uiFont != 0 {
			win.DeleteObject(win.HGDIOBJ(uiFont))
		}
		if titleFont != 0 && titleFont != uiFont {
			win.DeleteObject(win.HGDIOBJ(titleFont))
		}
		if sectionFont != 0 && sectionFont != uiFont && sectionFont != titleFont {
			win.DeleteObject(win.HGDIOBJ(sectionFont))
		}
		for _, brush := range customBrushes {
			if brush != 0 {
				win.DeleteObject(win.HGDIOBJ(brush))
			}
		}
		win.PostQuitMessage(0)
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

// createControls 创建主窗口上的标题区、筛选工具栏、任务列表和右侧详情面板。
func createControls(hwnd win.HWND) {
	headerBand = createLabel(hwnd, "", 0, 0, 1320, 60)
	titleLabel = createLabel(hwnd, "Windows 定时任务管理器", 22, 8, 440, 30)
	setControlFont(titleLabel, titleFont)
	subtitleLabel = createLabel(hwnd, "本机任务计划程序 · 离线管理", 22, 36, 420, 20)
	permissionText := "管理员模式 · 新建任务使用 SYSTEM / 最高权限"
	if !isElevated() {
		permissionText = "标准权限 · 部分系统任务可能无法管理"
	}
	permissionLabel = createLabel(hwnd, permissionText, 820, 20, 460, 22)
	statLabel = createLabel(hwnd, "共 0 个任务   ·   启用 0   ·   禁用 0   ·   当前显示 0", 22, 67, 720, 20)

	searchLabel = createLabel(hwnd, "搜索", 22, 96, 42, 24)
	setControlFont(searchLabel, sectionFont)
	searchEdit = createWindowEx(win.WS_EX_CLIENTEDGE, "EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.ES_AUTOHSCROLL, 68, 91, 280, 30, hwnd, idSearch)
	win.SendMessage(searchEdit, win.EM_SETCUEBANNER, 1, uintptr(unsafe.Pointer(utf16("名称、路径、描述或执行命令"))))
	searchScopeLabel = createLabel(hwnd, "搜索范围", 360, 96, 68, 24)
	setControlFont(searchScopeLabel, sectionFont)
	filterFieldCombo = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 432, 91, 118, 220, hwnd, idFilterField)
	comboAdd(filterFieldCombo, "全部字段")
	comboAdd(filterFieldCombo, "名称")
	comboAdd(filterFieldCombo, "描述")
	comboAdd(filterFieldCombo, "位置")
	comboAdd(filterFieldCombo, "执行路径")
	win.SendMessage(filterFieldCombo, win.CB_SETCURSEL, 0, 0)
	statusCombo = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 560, 91, 116, 220, hwnd, idStatus)
	comboAdd(statusCombo, "全部状态")
	comboAdd(statusCombo, "仅启用")
	comboAdd(statusCombo, "仅禁用")
	win.SendMessage(statusCombo, win.CB_SETCURSEL, 0, 0)

	backupButton = createButton(hwnd, "备份全部", 798, 91, 88, 30, idBackup)
	exportButton = createButton(hwnd, "导出", 894, 91, 76, 30, idExport)
	importButton = createButton(hwnd, "导入", 978, 91, 76, 30, idImport)
	refreshButton = createButton(hwnd, "刷新", 1110, 91, 76, 30, idRefresh)
	createButtonHwnd = createButton(hwnd, "新建任务", 1194, 91, 96, 30, idCreate)

	actionLabel = createLabel(hwnd, "选中任务", 22, 132, 82, 24)
	setControlFont(actionLabel, sectionFont)
	editButton = createButton(hwnd, "修改", 108, 127, 70, 30, idEdit)
	runButton = createButton(hwnd, "运行", 186, 127, 70, 30, idRun)
	endButton = createButton(hwnd, "停止", 264, 127, 70, 30, idEnd)
	enableButton = createButton(hwnd, "启用", 342, 127, 70, 30, idEnable)
	disableButton = createButton(hwnd, "禁用", 420, 127, 70, 30, idDisable)
	deleteButton = createButton(hwnd, "删除", 498, 127, 70, 30, idDelete)

	listView = createWindow(
		"SysListView32",
		"",
		win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.LVS_REPORT|win.LVS_SINGLESEL|win.LVS_SHOWSELALWAYS,
		22,
		164,
		860,
		540,
		hwnd,
		idList,
	)
	win.SendMessage(listView, win.LVM_SETEXTENDEDLISTVIEWSTYLE, 0, win.LVS_EX_FULLROWSELECT|win.LVS_EX_DOUBLEBUFFER|win.LVS_EX_LABELTIP|win.LVS_EX_INFOTIP|win.LVS_EX_HEADERDRAGDROP)
	if win.IsAppThemed() {
		win.SetWindowTheme(listView, utf16("Explorer"), nil)
	}
	win.SendMessage(listView, win.LVM_SETBKCOLOR, 0, uintptr(rgb(255, 255, 255)))
	win.SendMessage(listView, win.LVM_SETTEXTBKCOLOR, 0, uintptr(rgb(255, 255, 255)))
	win.SendMessage(listView, win.LVM_SETTEXTCOLOR, 0, uintptr(rgb(15, 23, 42)))
	addColumn(0, "任务名称", 220)
	addColumn(1, "状态", 82)
	addColumn(2, "下次运行", 145)
	addColumn(3, "上次运行", 145)
	addColumn(4, "结果", 88)
	addColumn(5, "触发器", 150)
	addColumn(6, "位置", 190)
	addColumn(7, "执行命令", 440)

	detailGroup = createLabel(hwnd, "任务详情", 900, 164, 390, 24)
	setControlFont(detailGroup, sectionFont)
	detailEdit = createWindowEx(
		win.WS_EX_CLIENTEDGE,
		"EDIT",
		"选择一条任务后，这里会显示完整配置。",
		win.WS_CHILD|win.WS_VISIBLE|win.ES_MULTILINE|win.ES_AUTOVSCROLL|win.ES_READONLY|win.WS_VSCROLL,
		914,
		194,
		362,
		500,
		hwnd,
		0,
	)
	updateActionButtons(nil)
}

func layoutControls(hwnd win.HWND) {
	var rect win.RECT
	win.GetClientRect(hwnd, &rect)
	width := int32(rect.Right - rect.Left)
	height := int32(rect.Bottom - rect.Top)
	if listView != 0 {
		const margin int32 = 22
		contentTop := int32(164)
		contentHeight := height - contentTop - margin
		detailWidth := width / 3
		if detailWidth < 330 {
			detailWidth = 330
		}
		if detailWidth > 420 {
			detailWidth = 420
		}
		gap := int32(16)
		listWidth := width - margin*2 - gap - detailWidth
		detailLeft := margin + listWidth + gap

		win.MoveWindow(headerBand, 0, 0, width, 60, true)
		win.MoveWindow(titleLabel, margin, 8, width-520, 30, true)
		win.MoveWindow(subtitleLabel, margin, 36, width-520, 20, true)
		win.MoveWindow(permissionLabel, width-500, 20, 478, 22, true)
		win.MoveWindow(statLabel, margin, 67, width-44, 20, true)
		win.MoveWindow(searchLabel, margin, 96, 42, 24, true)
		win.MoveWindow(searchEdit, 68, 91, 280, 30, true)
		win.MoveWindow(searchScopeLabel, 360, 96, 68, 24, true)
		win.MoveWindow(filterFieldCombo, 432, 91, 118, 220, true)
		win.MoveWindow(statusCombo, 560, 91, 116, 220, true)
		win.MoveWindow(backupButton, width-522, 91, 88, 30, true)
		win.MoveWindow(exportButton, width-426, 91, 76, 30, true)
		win.MoveWindow(importButton, width-330, 91, 76, 30, true)
		win.MoveWindow(refreshButton, width-202, 91, 76, 30, true)
		win.MoveWindow(createButtonHwnd, width-118, 91, 96, 30, true)
		win.MoveWindow(actionLabel, margin, 132, 82, 24, true)
		win.MoveWindow(editButton, 108, 127, 70, 30, true)
		win.MoveWindow(runButton, 186, 127, 70, 30, true)
		win.MoveWindow(endButton, 264, 127, 70, 30, true)
		win.MoveWindow(enableButton, 342, 127, 70, 30, true)
		win.MoveWindow(disableButton, 420, 127, 70, 30, true)
		win.MoveWindow(deleteButton, 498, 127, 70, 30, true)
		win.MoveWindow(listView, margin, contentTop, listWidth, contentHeight, true)
		win.MoveWindow(detailGroup, detailLeft, contentTop, detailWidth, 24, true)
		win.MoveWindow(detailEdit, detailLeft, contentTop+30, detailWidth, contentHeight-30, true)
	}
}

func handleCommand(id, code uint16) {
	switch id {
	case idSearch:
		if code == win.EN_CHANGE {
			applyFilter()
		}
	case idFilterField:
		if code == win.CBN_SELCHANGE {
			applyFilter()
		}
	case idStatus:
		if code == win.CBN_SELCHANGE {
			applyFilter()
		}
	case idRefresh:
		refreshTasksAsync()
	case idImport:
		chooseAndImportTasks()
	case idExport:
		exportSelectedOrFiltered()
	case idBackup:
		backupAllTasks()
	case idCreate:
		debugLog("new task button clicked")
		wizardInitialConfig = nil
		wizardOriginalName = ""
		win.PostMessage(mainWnd, msgCreateTask, 0, 0)
	case idEdit:
		editSelectedTask()
	case idRun:
		taskAction("run")
	case idEnd:
		taskAction("end")
	case idEnable:
		taskAction("enable")
	case idDisable:
		taskAction("disable")
	case idDelete:
		taskAction("delete")
	}
}

func handleNotify(lParam uintptr) uintptr {
	if lParam == 0 {
		return 0
	}
	// WM_NOTIFY 的 lParam 由系统传入 NMHDR 指针，这是 Win32 通知消息的固定结构。
	hdr := (*win.NMHDR)(unsafe.Pointer(lParam))
	if hdr.HwndFrom != listView {
		return 0
	}
	if hdr.Code == win.LVN_ITEMCHANGED {
		updateTaskDetailFromSelection()
		return 0
	}
	if hdr.Code == win.NM_CUSTOMDRAW {
		customDraw := (*win.NMLVCUSTOMDRAW)(unsafe.Pointer(lParam))
		switch customDraw.Nmcd.DwDrawStage {
		case win.CDDS_PREPAINT:
			return win.CDRF_NOTIFYITEMDRAW
		case win.CDDS_ITEMPREPAINT:
			index := int(customDraw.Nmcd.DwItemSpec)
			if index >= 0 && index < len(viewTasks) && customDraw.Nmcd.UItemState&win.CDIS_SELECTED == 0 {
				task := viewTasks[index]
				switch {
				case !task.Enabled:
					customDraw.ClrText = rgb(100, 116, 139)
					customDraw.ClrTextBk = rgb(248, 250, 252)
				case strings.EqualFold(task.Status, "Running") || task.Status == "正在运行":
					customDraw.ClrText = rgb(22, 101, 52)
					customDraw.ClrTextBk = rgb(240, 253, 244)
				case index%2 == 1:
					customDraw.ClrText = rgb(15, 23, 42)
					customDraw.ClrTextBk = rgb(248, 250, 252)
				default:
					customDraw.ClrText = rgb(15, 23, 42)
					customDraw.ClrTextBk = rgb(255, 255, 255)
				}
			}
			return win.CDRF_NEWFONT
		}
	}
	return 0
}

func safeCreateBasicTask() {
	defer func() {
		if r := recover(); r != nil {
			debugLog("panic in createBasicTask: %v\n%s", r, string(debug.Stack()))
			message(mainWnd, "保存任务失败", fmt.Sprintf("程序捕获到异常：%v\n\n详情见 debug.log", r))
		}
	}()
	createBasicTask()
}

func showTaskWizard() {
	if wizardWnd != 0 {
		win.ShowWindow(wizardWnd, win.SW_SHOW)
		win.SetForegroundWindow(wizardWnd)
		return
	}
	className := utf16("TaskSchedulerCreateWizard")
	var wc win.WNDCLASSEX
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wizardWndProc)
	wc.HInstance = hInst
	wc.HCursor = win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW))
	wc.HbrBackground = pageBrush
	wc.LpszClassName = className
	win.RegisterClassEx(&wc)

	windowTitle := "创建基本任务"
	if wizardInitialConfig != nil {
		windowTitle = "修改基本任务"
	}
	wizardWnd = win.CreateWindowEx(
		win.WS_EX_DLGMODALFRAME,
		className,
		utf16(windowTitle),
		win.WS_OVERLAPPED|win.WS_CAPTION|win.WS_SYSMENU|win.WS_VISIBLE,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
		800,
		610,
		mainWnd,
		0,
		hInst,
		nil,
	)
	if wizardWnd == 0 {
		message(mainWnd, "新建任务失败", "创建任务配置窗口失败。")
	}
}

func refreshTasks() {
	tasks, err := queryTasks()
	if err != nil {
		message(mainWnd, "读取失败", err.Error())
		return
	}
	allTasks = tasks
	applyFilter()
}

// refreshTasksAsync 在后台读取任务，避免 schtasks 查询大量任务时卡住界面线程。
func refreshTasksAsync() {
	if loadingTasks || operationBusy {
		return
	}
	debugLog("refreshTasksAsync start")
	loadingTasks = true
	loadingStartedAt = time.Now()
	setMainToolbarEnabled(false)
	updateActionButtons(nil)
	setWindowText(statLabel, "正在读取定时任务，请稍候...")
	if listView != 0 {
		win.SendMessage(listView, win.LVM_DELETEALLITEMS, 0, 0)
	}
	go func() {
		tasks, err := queryTasks()
		if err != nil {
			debugLog("refreshTasksAsync failed: %v", err)
			lastLoadError = err.Error()
			win.PostMessage(mainWnd, msgTasksFailed, 0, 0)
			return
		}
		debugLog("refreshTasksAsync loaded: %d tasks", len(tasks))
		pendingTasks = tasks
		win.PostMessage(mainWnd, msgTasksLoaded, 0, 0)
	}()
}

// applyFilter 根据搜索框、筛选字段和启用状态生成当前列表 viewTasks。
func applyFilter() {
	query := strings.ToLower(strings.TrimSpace(windowText(searchEdit)))
	field := comboText(filterFieldCombo)
	stateIndex := int(win.SendMessage(statusCombo, win.CB_GETCURSEL, 0, 0))
	viewTasks = viewTasks[:0]
	enabled := 0
	for _, task := range allTasks {
		if task.Enabled {
			enabled++
		}
		if query != "" && !taskMatches(task, field, query) {
			continue
		}
		if stateIndex == 1 && !task.Enabled {
			continue
		}
		if stateIndex == 2 && task.Enabled {
			continue
		}
		viewTasks = append(viewTasks, task)
	}
	renderList()
	setWindowText(statLabel, fmt.Sprintf("共 %d 个任务   ·   启用 %d   ·   禁用 %d   ·   当前显示 %d", len(allTasks), enabled, len(allTasks)-enabled, len(viewTasks)))
}

// renderList 将 viewTasks 渲染到 Win32 ListView。
func renderList() {
	win.SendMessage(listView, win.LVM_DELETEALLITEMS, 0, 0)
	for i, task := range viewTasks {
		values := []string{task.ShortName, task.Status, task.NextRunTime, task.LastRunTime, task.LastResult, task.ScheduleType, task.Location, task.TaskToRun}
		insertItem(i, values)
	}
	if len(viewTasks) > 0 {
		item := win.LVITEM{StateMask: win.LVIS_SELECTED, State: win.LVIS_SELECTED}
		win.SendMessage(listView, win.LVM_SETITEMSTATE, 0, uintptr(unsafe.Pointer(&item)))
		updateTaskDetail(viewTasks[0])
		return
	}
	updateTaskDetail()
}

func updateTaskDetailFromSelection() {
	index := int(win.SendMessage(listView, win.LVM_GETNEXTITEM, ^uintptr(0), win.LVNI_SELECTED))
	if index >= 0 && index < len(viewTasks) {
		updateTaskDetail(viewTasks[index])
		return
	}
	updateTaskDetail()
}

func updateTaskDetail(tasks ...Task) {
	if detailEdit == 0 {
		return
	}
	if len(tasks) == 0 {
		if len(viewTasks) == 1 {
			updateTaskDetail(viewTasks[0])
			return
		}
		setWindowText(detailEdit, "选择一条任务后，这里会显示完整配置。\r\n\r\n提示：浅绿色表示正在运行，灰色表示已禁用。")
		updateActionButtons(nil)
		return
	}
	task := tasks[0]
	scriptPath := taskScriptPath(task)
	text := fmt.Sprintf(
		"%s\r\n\r\n状态\r\n  %s\r\n\r\n触发器\r\n  %s\r\n\r\n运行时间\r\n  下次：%s\r\n  上次：%s\r\n  结果：%s\r\n\r\n执行命令\r\n  %s\r\n\r\n脚本路径\r\n  %s\r\n\r\n起始目录\r\n  %s\r\n\r\n任务位置\r\n  %s",
		task.ShortName,
		detailValue(task.Status),
		detailValue(task.ScheduleType),
		detailValue(task.NextRunTime),
		detailValue(task.LastRunTime),
		detailValue(task.LastResult),
		detailValue(task.TaskToRun),
		detailValue(scriptPath),
		detailValue(task.StartIn),
		detailValue(task.Name),
	)
	if strings.TrimSpace(task.Comment) != "" && task.Comment != "N/A" {
		text += "\r\n\r\n描述\r\n  " + task.Comment
	}
	setWindowText(detailEdit, text)
	updateActionButtons(&task)
}

func detailValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "N/A") {
		return "—"
	}
	return value
}

func updateActionButtons(task *Task) {
	hasTask := task != nil && !loadingTasks && !operationBusy
	win.EnableWindow(editButton, hasTask)
	win.EnableWindow(runButton, hasTask)
	win.EnableWindow(deleteButton, hasTask)
	if !hasTask {
		win.EnableWindow(endButton, false)
		win.EnableWindow(enableButton, false)
		win.EnableWindow(disableButton, false)
		return
	}
	running := strings.EqualFold(task.Status, "Running") || task.Status == "正在运行"
	win.EnableWindow(endButton, running)
	win.EnableWindow(enableButton, !task.Enabled)
	win.EnableWindow(disableButton, task.Enabled)
}

func handleWatchdogTick() {
	now := time.Now()
	if lastWatchdogLog.IsZero() || now.Sub(lastWatchdogLog) >= 30*time.Second {
		debugLog("ui watchdog heartbeat loading=%v operation=%v wizardSaving=%v", loadingTasks, operationBusy, wizardSaving)
		lastWatchdogLog = now
	}
	if loadingTasks && !loadingStartedAt.IsZero() && now.Sub(loadingStartedAt) > watchdogStaleAfter {
		debugLog("watchdog reset stale loading state after %s", now.Sub(loadingStartedAt).Round(time.Second))
		loadingTasks = false
		loadingStartedAt = time.Time{}
		pendingTasks = nil
		setMainToolbarEnabled(true)
		setWindowText(statLabel, "读取任务超时，请点击刷新重试。")
		updateTaskDetailFromSelection()
	}
	if operationBusy && !archiveOperation && !operationStartedAt.IsZero() && now.Sub(operationStartedAt) > watchdogStaleAfter {
		debugLog("watchdog reset stale operation state after %s", now.Sub(operationStartedAt).Round(time.Second))
		operationBusy = false
		operationStartedAt = time.Time{}
		wizardSaving = false
		setMainToolbarEnabled(true)
		if wizardSubmitButton != 0 {
			win.EnableWindow(wizardSubmitButton, true)
		}
		setWindowText(statLabel, "操作超时，界面已恢复。请查看 debug.log 或刷新后重试。")
		updateTaskDetailFromSelection()
	}
}

func taskScriptPath(task Task) string {
	parts := splitCommandLine(task.TaskToRun)
	for i, part := range parts {
		if strings.EqualFold(part, "-f") && i+1 < len(parts) {
			return absoluteTaskPath(parts[i+1], task.StartIn)
		}
	}
	if len(parts) > 0 {
		return absoluteTaskPath(parts[0], task.StartIn)
	}
	return ""
}

func absoluteTaskPath(pathValue, startIn string) string {
	pathValue = strings.Trim(strings.TrimSpace(pathValue), `"`)
	startIn = strings.Trim(strings.TrimSpace(startIn), `"`)
	if pathValue == "" || strings.EqualFold(pathValue, "N/A") {
		return ""
	}
	if filepath.IsAbs(pathValue) {
		return filepath.Clean(pathValue)
	}
	if startIn != "" && !strings.EqualFold(startIn, "N/A") {
		return filepath.Clean(filepath.Join(startIn, pathValue))
	}
	return pathValue
}

func splitCommandLine(command string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range command {
		switch r {
		case '"':
			inQuote = !inQuote
		case ' ', '\t':
			if inQuote {
				b.WriteRune(r)
				continue
			}
			if b.Len() > 0 {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// taskMatches 判断任务是否命中当前筛选条件。
func taskMatches(task Task, field, query string) bool {
	var values []string
	switch field {
	case "\u540d\u79f0":
		values = []string{task.ShortName, task.Name}
	case "\u63cf\u8ff0":
		values = []string{task.Comment}
	case "\u4f4d\u7f6e":
		values = []string{task.Location, task.Name, task.StartIn}
	case "\u6267\u884c\u8def\u5f84":
		values = []string{task.TaskToRun, task.StartIn, taskScriptPath(task)}
	default:
		values = []string{
			task.Name,
			task.ShortName,
			task.Location,
			task.Status,
			task.TaskToRun,
			taskScriptPath(task),
			task.StartIn,
			task.Comment,
			task.ScheduleType,
		}
	}
	return strings.Contains(strings.ToLower(strings.Join(values, " ")), query)
}

func selectedTask() (Task, bool) {
	index := int(win.SendMessage(listView, win.LVM_GETNEXTITEM, ^uintptr(0), win.LVNI_SELECTED))
	if index < 0 || index >= len(viewTasks) {
		message(mainWnd, "未选择任务", "请先选择一个任务。")
		return Task{}, false
	}
	return viewTasks[index], true
}

func editSelectedTask() {
	if wizardWnd != 0 {
		message(mainWnd, "正在编辑", "请先保存或关闭当前任务窗口。")
		return
	}
	if operationBusy || loadingTasks {
		return
	}
	task, ok := selectedTask()
	if !ok {
		return
	}
	operationBusy = true
	operationStartedAt = time.Now()
	setMainToolbarEnabled(false)
	updateActionButtons(nil)
	setWindowText(statLabel, "正在读取任务定义，请稍候...")
	taskName := task.Name
	go func() {
		config, err := loadTaskConfig(taskName)
		if err != nil {
			lastEditError = err.Error()
			win.PostMessage(mainWnd, msgEditFailed, 0, 0)
			return
		}
		pendingEditConfig = config
		pendingEditOriginal = taskName
		win.PostMessage(mainWnd, msgEditLoaded, 0, 0)
	}()
}

func loadTaskConfig(name string) (TaskCreateConfig, error) {
	out, err := exportTaskXML(name)
	if err != nil {
		return TaskCreateConfig{}, fmt.Errorf("读取任务定义失败：%s", err.Error())
	}
	return parseTaskXMLConfig(name, out)
}

func parseTaskXMLConfig(name, xmlText string) (TaskCreateConfig, error) {
	xmlText = normalizeTaskXMLText(xmlText)
	var definition schedulerTaskXML
	if err := xml.Unmarshal([]byte(xmlText), &definition); err != nil {
		return TaskCreateConfig{}, fmt.Errorf("解析任务 XML 失败：%w", err)
	}

	actionCount := len(definition.Actions.Exec) + len(definition.Actions.ComHandler) + len(definition.Actions.SendEmail) + len(definition.Actions.ShowMessage)
	if actionCount != 1 || len(definition.Actions.Exec) != 1 {
		return TaskCreateConfig{}, errors.New("该任务包含多个操作或非程序操作，请使用 Windows 任务计划程序修改。")
	}
	triggerCount := len(definition.Triggers.Time) + len(definition.Triggers.Calendar) + len(definition.Triggers.Event) + len(definition.Triggers.Boot) + len(definition.Triggers.Logon) + len(definition.Triggers.Idle) + len(definition.Triggers.Registration) + len(definition.Triggers.Session)
	if triggerCount != 1 {
		return TaskCreateConfig{}, errors.New("该任务包含多个触发器或高级触发器，请使用 Windows 任务计划程序修改。")
	}

	action := definition.Actions.Exec[0]
	config := TaskCreateConfig{
		Name:        name,
		Description: definition.RegistrationInfo.Description,
		Program:     action.Command,
		Arguments:   action.Arguments,
		StartIn:     action.WorkingDirectory,
	}
	var err error
	switch {
	case len(definition.Triggers.Time) == 1:
		trigger := definition.Triggers.Time[0]
		config.RepeatIntervalMinutes, config.RepeatDurationMinutes, err = parseXMLRepetition(trigger.Repetition)
		if err != nil {
			return TaskCreateConfig{}, err
		}
		config.Schedule = "一次"
		config.StartDate, config.StartTime, err = splitStartBoundary(trigger.StartBoundary)
	case len(definition.Triggers.Calendar) == 1:
		trigger := definition.Triggers.Calendar[0]
		config.RepeatIntervalMinutes, config.RepeatDurationMinutes, err = parseXMLRepetition(trigger.Repetition)
		if err != nil {
			return TaskCreateConfig{}, err
		}
		config.StartDate, config.StartTime, err = splitStartBoundary(trigger.StartBoundary)
		if err != nil {
			break
		}
		scheduleCount := 0
		if trigger.ScheduleByDay != nil {
			scheduleCount++
			if trigger.ScheduleByDay.DaysInterval > 1 {
				return TaskCreateConfig{}, errors.New("该任务不是每天执行一次，当前编辑器暂不支持。")
			}
			config.Schedule = "每天"
		}
		if trigger.ScheduleByWeek != nil {
			scheduleCount++
			if trigger.ScheduleByWeek.WeeksInterval > 1 {
				return TaskCreateConfig{}, errors.New("该任务不是每周执行，当前编辑器暂不支持。")
			}
			config.Schedule = "每周"
			config.WeekDays = xmlWeekDays(trigger.ScheduleByWeek.DaysOfWeek)
			if len(config.WeekDays) == 0 {
				return TaskCreateConfig{}, errors.New("任务 XML 中没有可识别的每周日期。")
			}
		}
		if trigger.ScheduleByMonth != nil {
			scheduleCount++
			if trigger.ScheduleByMonth.RunOnLastDayOfMonth {
				config.MonthDay = "LASTDAY"
			} else if len(trigger.ScheduleByMonth.DaysOfMonth.Days) == 1 {
				config.MonthDay, err = normalizeMonthDay(trigger.ScheduleByMonth.DaysOfMonth.Days[0])
				if err != nil {
					return TaskCreateConfig{}, err
				}
			} else {
				return TaskCreateConfig{}, errors.New("该任务包含多个每月日期，当前编辑器暂不支持。")
			}
			config.Schedule = "每月"
		}
		if scheduleCount != 1 {
			return TaskCreateConfig{}, errors.New("无法识别该日历触发器，请使用 Windows 任务计划程序修改。")
		}
	case len(definition.Triggers.Event) == 1:
		trigger := definition.Triggers.Event[0]
		if trigger.Repetition != nil || !strings.Contains(trigger.Subscription, "1074") || !strings.Contains(strings.ToUpper(trigger.Subscription), "USER32") {
			return TaskCreateConfig{}, errors.New("该事件触发器不是受支持的关机事件。")
		}
		config.Schedule = "关机时"
	default:
		return TaskCreateConfig{}, errors.New("当前编辑器不支持该任务的触发器类型。")
	}
	if err != nil {
		return TaskCreateConfig{}, err
	}
	return config, nil
}

func splitStartBoundary(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 16 || value[10] != 'T' {
		return "", "", errors.New("任务开始日期格式无法识别。")
	}
	parsed, err := time.Parse("2006-01-02T15:04", value[:16])
	if err != nil {
		return "", "", errors.New("任务开始日期格式无法识别。")
	}
	return parsed.Format("2006/01/02"), parsed.Format("15:04"), nil
}

func xmlWeekDays(days schedulerDaysOfWeek) []string {
	var result []string
	for _, item := range []struct {
		code    string
		present bool
	}{
		{"SUN", days.Sunday != nil}, {"MON", days.Monday != nil}, {"TUE", days.Tuesday != nil},
		{"WED", days.Wednesday != nil}, {"THU", days.Thursday != nil}, {"FRI", days.Friday != nil}, {"SAT", days.Saturday != nil},
	} {
		if item.present {
			result = append(result, item.code)
		}
	}
	return result
}

// taskAction 将运行、停止、启用、禁用、删除转换为 schtasks.exe 操作。
func taskAction(action string) {
	if operationBusy || loadingTasks {
		return
	}
	task, ok := selectedTask()
	if !ok {
		return
	}
	if action == "delete" {
		ret := win.MessageBox(mainWnd, utf16("确定删除任务："+task.Name+" ?"), utf16("确认删除"), win.MB_YESNO|win.MB_ICONQUESTION)
		if ret != win.IDYES {
			return
		}
	}
	args, err := actionArgs(action, task.Name)
	if err != nil {
		message(mainWnd, "操作失败", err.Error())
		return
	}
	operationBusy = true
	operationStartedAt = time.Now()
	setMainToolbarEnabled(false)
	updateActionButtons(nil)
	setWindowText(statLabel, "正在执行操作，请稍候...")
	go func() {
		output, err := runSchtasks(args...)
		text := strings.TrimSpace(output)
		if err != nil {
			if text == "" {
				text = err.Error()
			}
			lastActionTitle = "操作失败"
			lastActionMessage = text
			win.PostMessage(mainWnd, msgActionFailed, 0, 0)
			return
		}
		if text == "" {
			text = "操作已完成。"
		}
		lastActionTitle = "操作完成"
		lastActionMessage = text
		win.PostMessage(mainWnd, msgActionDone, 0, 0)
	}()
}

func setMainToolbarEnabled(enabled bool) {
	for _, button := range []win.HWND{refreshButton, createButtonHwnd, importButton, exportButton, backupButton} {
		if button != 0 {
			win.EnableWindow(button, enabled)
		}
	}
}

func beginArchiveOperation(status string) {
	operationBusy = true
	archiveOperation = true
	operationStartedAt = time.Now()
	setMainToolbarEnabled(false)
	updateActionButtons(nil)
	setWindowText(statLabel, status)
}

func finishArchiveOperation(title, text string, refresh bool) {
	debugLog("finishArchiveOperation start title=%q refresh=%v", title, refresh)
	operationBusy = false
	archiveOperation = false
	operationStartedAt = time.Time{}
	setMainToolbarEnabled(true)
	pendingImportPlan = nil
	pendingArchiveError = ""
	debugLog("finishArchiveOperation before message")
	message(mainWnd, title, text)
	debugLog("finishArchiveOperation after message")
	if refresh {
		debugLog("finishArchiveOperation starts async refresh")
		refreshTasksAsync()
		return
	}
	debugLog("finishArchiveOperation updates selected detail")
	updateTaskDetailFromSelection()
}

// 备份/导出完成后统一走异步刷新，避免在模态完成提示后同步访问 ListView。
func archiveCompletionRefresh() bool {
	return true
}

func selectedTaskForExport() *Task {
	index := int(win.SendMessage(listView, win.LVM_GETNEXTITEM, ^uintptr(0), win.LVNI_SELECTED))
	if index < 0 || index >= len(viewTasks) {
		return nil
	}
	selected := viewTasks[index]
	return &selected
}

func tasksForExport(selected *Task, filtered []Task) []Task {
	if selected != nil {
		return []Task{*selected}
	}
	return append([]Task(nil), filtered...)
}

func exportSelectedOrFiltered() {
	if operationBusy || loadingTasks {
		return
	}
	tasks := tasksForExport(selectedTaskForExport(), viewTasks)
	if len(tasks) == 0 {
		message(mainWnd, "无法导出", "当前没有可导出的任务。")
		return
	}
	path := saveTaskArchiveDialog("导出计划任务", "TaskExport_"+time.Now().Format("20060102_150405")+".zip")
	if path == "" {
		return
	}
	exportTasksAsync(tasks, path, "正在导出任务，请稍候...")
}

func backupAllTasks() {
	if operationBusy || loadingTasks {
		return
	}
	tasks := append([]Task(nil), allTasks...)
	if len(tasks) == 0 {
		message(mainWnd, "无法备份", "本机没有可备份的任务。")
		return
	}
	path := saveTaskArchiveDialog("备份全部计划任务", "TaskBackup_"+time.Now().Format("20060102_150405")+".zip")
	if path == "" {
		return
	}
	exportTasksAsync(tasks, path, "正在备份全部任务，请稍候...")
}

func exportTasksAsync(tasks []Task, outputPath, status string) {
	beginArchiveOperation(status)
	go func() {
		debugLog("archive export started taskCount=%d path=%q", len(tasks), outputPath)
		summary := collectTaskArchiveItems(tasks, exportTaskXML)
		debugLog("archive export XML collection finished success=%d total=%d", len(summary.Items), len(summary.Results))
		if len(summary.Items) == 0 {
			pendingArchiveError = "没有任务能够导出。\r\n\r\n" + archiveResultSummary(summary.Results, "")
		} else if err := writeTaskArchive(outputPath, "", summary.Items); err != nil {
			pendingArchiveError = err.Error()
		} else {
			pendingArchivePath = outputPath
			debugLog("archive export ZIP write finished path=%q", outputPath)
		}
		pendingTransfer = summary.Results
		debugLog("archive export posts completion error=%q", pendingArchiveError)
		win.PostMessage(mainWnd, msgArchiveDone, 0, 0)
	}()
}

func chooseAndImportTasks() {
	if operationBusy || loadingTasks {
		return
	}
	path := openTaskImportDialog()
	if path == "" {
		return
	}
	startImportPreflight(path)
}

func startImportPreflight(inputPath string) {
	beginArchiveOperation("正在检查导入文件，请稍候...")
	knownTasks := make(map[string]bool, len(allTasks))
	for _, task := range allTasks {
		if name, err := normalizeTaskName(task.Name); err == nil {
			knownTasks[name] = true
		}
	}
	go func() {
		items, err := readTaskImportFile(inputPath)
		if err != nil {
			pendingArchiveError = err.Error()
			pendingImportPlan = nil
		} else {
			pendingArchiveError = ""
			pendingImportPlan = buildTaskImportPlan(items, knownTasks)
		}
		win.PostMessage(mainWnd, msgImportReady, 0, 0)
	}()
}

func confirmImportPlan(plan []TaskImportPlanItem) bool {
	if len(plan) == 0 {
		message(mainWnd, "无法导入", "导入文件中没有任务。")
		return false
	}
	message(mainWnd, "导入预检", importPlanSummary(plan))
	for index := range plan {
		if !plan[index].Exists {
			continue
		}
		text := "目标服务器已存在同名任务：\r\n" + plan[index].Item.Name + "\r\n\r\n选择“是”覆盖，选择“否”跳过，选择“取消”终止本次导入。"
		result := win.MessageBox(mainWnd, utf16(text), utf16("导入冲突"), win.MB_YESNOCANCEL|win.MB_ICONQUESTION)
		switch result {
		case win.IDYES:
			plan[index].Overwrite = true
		case win.IDNO:
			plan[index].Skip = true
		default:
			return false
		}
	}
	pendingImportPlan = plan
	return true
}

func startTaskImportAsync(plan []TaskImportPlanItem) {
	setWindowText(statLabel, "正在导入任务，请稍候...")
	go func() {
		pendingTransfer = applyTaskImportPlan(plan, restoreTaskXML)
		win.PostMessage(mainWnd, msgImportDone, 0, 0)
	}()
}

func importPlanSummary(plan []TaskImportPlanItem) string {
	var lines []string
	conflicts := 0
	for _, item := range plan {
		if item.Exists {
			conflicts++
		}
		command := "—"
		var definition schedulerTaskXML
		if xml.Unmarshal([]byte(item.Item.XML), &definition) == nil && len(definition.Actions.Exec) == 1 {
			command = strings.TrimSpace(definition.Actions.Exec[0].Command + " " + definition.Actions.Exec[0].Arguments)
		}
		lines = append(lines, item.Item.Name+"\r\n  "+command)
	}
	if len(lines) > 12 {
		lines = append(lines[:12], fmt.Sprintf("……其余 %d 个任务", len(plan)-12))
	}
	return fmt.Sprintf("共检测到 %d 个任务，其中同名冲突 %d 个。\r\n\r\n%s", len(plan), conflicts, strings.Join(lines, "\r\n"))
}

func archiveResultSummary(results []TaskTransferResult, outputPath string) string {
	success, skipped, failed := 0, 0, 0
	var details []string
	for _, result := range results {
		switch {
		case result.Err != nil:
			failed++
			if len(details) < 12 {
				details = append(details, result.Name+"："+result.Err.Error())
			}
		case result.Skipped:
			skipped++
		default:
			success++
		}
	}
	text := fmt.Sprintf("成功 %d 个，跳过 %d 个，失败 %d 个。", success, skipped, failed)
	if outputPath != "" {
		text = "文件已保存：\r\n" + outputPath + "\r\n\r\n" + text
	}
	if len(details) > 0 {
		text += "\r\n\r\n失败详情：\r\n" + strings.Join(details, "\r\n")
	}
	return text
}

// createBasicTask 从弹窗控件读取输入，构造 schtasks 参数并创建任务。
func createBasicTask() {
	if wizardSaving || operationBusy || loadingTasks {
		return
	}
	debugLog("createBasicTask start")
	cfg := TaskCreateConfig{
		Name:        strings.TrimSpace(windowText(wizardName)),
		Description: strings.TrimSpace(windowText(wizardDesc)),
		Schedule:    comboText(wizardSched),
		StartDate:   strings.TrimSpace(windowText(wizardDate)),
		StartTime:   strings.TrimSpace(windowText(wizardTime)),
		WeekDays:    selectedWeekDays(),
		MonthDay:    selectedMonthDay(),
	}
	if win.SendMessage(wizardRepeatCheck, win.BM_GETCHECK, 0, 0) == win.BST_CHECKED {
		cfg.RepeatIntervalMinutes = selectedRepeatIntervalMinutes()
		cfg.RepeatDurationMinutes = selectedRepeatDurationMinutes()
	}
	cfg.Program = strings.TrimSpace(windowText(wizardProgram))
	cfg.Arguments = strings.TrimSpace(windowText(wizardArgs))
	cfg.StartIn = strings.TrimSpace(windowText(wizardStartIn))
	originalName := strings.TrimSpace(wizardOriginalName)
	isEdit := originalName != ""
	wizardSaving = true
	operationBusy = true
	operationStartedAt = time.Now()
	if wizardSubmitButton != 0 {
		win.EnableWindow(wizardSubmitButton, false)
	}
	setMainToolbarEnabled(false)
	setWindowText(statLabel, "正在保存任务，请稍候...")
	go func() {
		if err := saveTaskConfigIsolated(cfg, originalName, true); err != nil {
			lastWizardTitle = "保存失败"
			lastWizardMessage = err.Error()
			win.PostMessage(mainWnd, msgWizardSaveFail, 0, 0)
			return
		}
		if isEdit {
			lastWizardTitle = "修改成功"
			lastWizardMessage = "任务已保存。"
		} else {
			lastWizardTitle = "创建成功"
			lastWizardMessage = "任务已创建。"
		}
		win.PostMessage(mainWnd, msgWizardSaved, 0, 0)
	}()
}

func saveTaskConfig(cfg TaskCreateConfig, originalName string, systemAccount bool) error {
	if err := validateSystemTaskPrivilege(systemAccount, isElevated()); err != nil {
		return err
	}
	newName, err := normalizeTaskName(cfg.Name)
	if err != nil {
		return err
	}
	originalName = strings.TrimSpace(originalName)
	isEdit := originalName != ""
	if isEdit {
		originalName, err = normalizeTaskName(originalName)
		if err != nil {
			return err
		}
	}
	if (!isEdit || !strings.EqualFold(originalName, newName)) && taskExists(newName) {
		return errors.New("目标任务名已存在，请换一个名称。")
	}
	args, err := buildCreateArgs(cfg)
	if err != nil {
		return err
	}
	if !systemAccount {
		args = removeSystemRunOptions(args)
	}
	debugLog("running schtasks create args=%q", strings.Join(args, " "))
	output, err := runSchtasks(args...)
	if err != nil {
		return errors.New(strings.TrimSpace(output))
	}
	if strings.TrimSpace(cfg.Description) != "" || strings.TrimSpace(cfg.StartIn) != "" {
		if err = updateTaskMetadata(newName, cfg.Description, cfg.StartIn); err != nil {
			debugLog("metadata update skipped after task save: %v", err)
			return errors.New("任务主体已保存，但描述或起始目录写入失败：" + err.Error())
		}
	}
	if isEdit && !strings.EqualFold(originalName, newName) {
		if deleteOutput, deleteErr := runSchtasks("/delete", "/tn", originalName, "/f"); deleteErr != nil {
			_, _ = runSchtasks("/delete", "/tn", newName, "/f")
			return errors.New("旧任务无法删除，新任务已回滚：" + strings.TrimSpace(deleteOutput))
		}
	}
	return nil
}

func validateSystemTaskPrivilege(systemAccount bool, elevated bool) error {
	if systemAccount && !elevated {
		return errors.New("创建 SYSTEM / 最高权限任务需要管理员权限。请以管理员身份重新运行本程序。")
	}
	return nil
}

func saveTaskConfigIsolated(cfg TaskCreateConfig, originalName string, systemAccount bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	requestFile, err := os.CreateTemp("", "task-scheduler-save-*.json")
	if err != nil {
		return err
	}
	requestPath := requestFile.Name()
	resultFile, err := os.CreateTemp("", "task-scheduler-save-result-*.txt")
	if err != nil {
		requestFile.Close()
		_ = os.Remove(requestPath)
		return err
	}
	resultPath := resultFile.Name()
	resultFile.Close()
	_ = os.Remove(resultPath)
	defer os.Remove(requestPath)
	defer os.Remove(resultPath)

	request := saveTaskRequest{Config: cfg, OriginalName: originalName, SystemAccount: systemAccount}
	if err = json.NewEncoder(requestFile).Encode(request); err != nil {
		requestFile.Close()
		return err
	}
	if err = requestFile.Close(); err != nil {
		return err
	}
	debugLog("starting isolated save helper: %s", requestPath)
	cmd := exec.Command(exe, "--save-task", requestPath, resultPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := runCommandWithTimeout(cmd, externalCommandTimeout)
	resultBytes, readErr := os.ReadFile(resultPath)
	result := strings.TrimSpace(string(resultBytes))
	if err != nil {
		if result != "" {
			return errors.New(result)
		}
		text := strings.TrimSpace(out)
		if text != "" {
			return errors.New(text)
		}
		return err
	}
	if readErr == nil && result != "" && result != "OK" {
		return errors.New(result)
	}
	return nil
}

func runSaveTaskCommand(args []string) error {
	if len(args) < 2 {
		return errors.New("missing save task command arguments")
	}
	requestPath := args[0]
	resultPath := args[1]
	data, err := os.ReadFile(requestPath)
	if err != nil {
		_ = os.WriteFile(resultPath, []byte(err.Error()), 0600)
		return err
	}
	var request saveTaskRequest
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err = json.Unmarshal(data, &request); err != nil {
		_ = os.WriteFile(resultPath, []byte(err.Error()), 0600)
		return err
	}
	err = saveTaskConfig(request.Config, request.OriginalName, request.SystemAccount)
	if err != nil {
		_ = os.WriteFile(resultPath, []byte(err.Error()), 0600)
		return err
	}
	_ = os.WriteFile(resultPath, []byte("OK"), 0600)
	return nil
}

// buildCreateArgs 将界面配置转换成 schtasks.exe /create 参数。
// 这里保持 Server 2008 R2 兼容，只使用 schtasks 常见参数。
func buildCreateArgs(cfg TaskCreateConfig) ([]string, error) {
	name, err := normalizeTaskName(cfg.Name)
	program := strings.TrimSpace(cfg.Program)
	if err != nil {
		return nil, err
	}
	if program == "" {
		return nil, errors.New("执行文件不能为空。")
	}
	schedule := strings.ToUpper(strings.TrimSpace(cfg.Schedule))
	schedule = scheduleCode(schedule)
	switch schedule {
	case "ONCE", "DAILY", "WEEKLY", "MONTHLY", "ONEVENT_SHUTDOWN":
	default:
		return nil, errors.New("不支持的计划类型。")
	}
	command := quoteIfNeeded(program)
	if strings.TrimSpace(cfg.Arguments) != "" {
		command += " " + strings.TrimSpace(cfg.Arguments)
	}
	if schedule == "ONEVENT_SHUTDOWN" {
		return []string{
			"/create",
			"/tn", name,
			"/tr", command,
			"/sc", "ONEVENT",
			"/ec", "System",
			"/mo", "*[System[Provider[@Name='USER32'] and EventID=1074]]",
			"/ru", "SYSTEM",
			"/rl", "HIGHEST",
			"/f",
		}, nil
	}
	startTime, err := normalizeTaskTime(cfg.StartTime)
	if err != nil {
		return nil, err
	}
	args := []string{"/create", "/tn", name, "/tr", command, "/sc", schedule, "/st", startTime, "/ru", "SYSTEM", "/rl", "HIGHEST", "/f"}
	startDate, err := normalizeTaskDate(cfg.StartDate)
	if err != nil {
		return nil, err
	}
	if startDate != "" {
		args = append(args, "/sd", startDate)
	} else if schedule == "ONCE" {
		return nil, errors.New("一次性任务必须填写开始日期。")
	}
	if schedule == "WEEKLY" {
		weekDays, err := normalizeWeekDays(cfg.WeekDays)
		if err != nil {
			return nil, err
		}
		args = append(args, "/d", strings.Join(weekDays, ","))
	}
	if schedule == "MONTHLY" {
		monthDay, err := normalizeMonthDay(cfg.MonthDay)
		if err != nil {
			return nil, err
		}
		if monthDay == "LASTDAY" {
			args = append(args, "/mo", "LASTDAY", "/m", "*")
		} else {
			args = append(args, "/d", monthDay)
		}
	}
	if cfg.RepeatIntervalMinutes > 0 {
		interval, duration, err := normalizeRepeatSettings(schedule, cfg.RepeatIntervalMinutes, cfg.RepeatDurationMinutes)
		if err != nil {
			return nil, err
		}
		args = append(args, "/ri", strconv.Itoa(interval), "/du", formatDurationForSchtasks(duration))
	}
	return args, nil
}

func normalizeTaskName(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "/", `\`))
	if value == "" {
		return "", errors.New("任务名不能为空。")
	}
	if !strings.HasPrefix(value, `\`) {
		value = `\` + value
	}
	if strings.HasSuffix(value, `\`) {
		return "", errors.New("任务名不能以反斜杠结尾。")
	}
	for _, part := range strings.Split(strings.TrimPrefix(value, `\`), `\`) {
		if strings.TrimSpace(part) == "" || strings.ContainsAny(part, `<>:"|?*`) {
			return "", errors.New("任务名包含空目录或非法字符。")
		}
	}
	return value, nil
}

func taskExists(name string) bool {
	_, err := runSchtasks("/query", "/tn", name)
	return err == nil
}

func exportTaskXML(name string) (string, error) {
	out, err := runSchtasks("/query", "/tn", name, "/xml")
	if err == nil {
		return normalizeTaskXMLText(out), nil
	}
	schtasksErr := strings.TrimSpace(out)
	if schtasksErr == "" {
		schtasksErr = err.Error()
	}
	xmlText, fallbackErr := exportTaskXMLViaPowerShell(name)
	if fallbackErr == nil {
		debugLog("schtasks XML export failed, COM fallback succeeded: %s", schtasksErr)
		return normalizeTaskXMLText(xmlText), nil
	}
	return "", fmt.Errorf("schtasks失败：%s；COM兜底失败：%v", schtasksErr, fallbackErr)
}

func exportTaskXMLViaPowerShell(name string) (string, error) {
	shortName, folderName := splitTaskPath(name)
	encode := func(value string) string {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
function Decode([string]$value) {
  [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($value))
}
$service = New-Object -ComObject Schedule.Service
$service.Connect()
$folder = $service.GetFolder((Decode '%s'))
$task = $folder.GetTask((Decode '%s'))
$xml = [string]$task.Xml
[Console]::Out.WriteLine([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($xml)))
`, encode(folderName), encode(shortName))
	out, err := runPowerShellWithTimeout(script, metadataCommandTimeout)
	if err != nil {
		text := strings.TrimSpace(out)
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return decodePowerShellBase64Output(out)
}

func decodePowerShellBase64Output(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		value, err := decodeBase64Field(line)
		if err == nil {
			return value, nil
		}
	}
	return "", errors.New("PowerShell COM 未返回任务 XML")
}

func normalizeTaskXMLText(value string) string {
	value = strings.TrimPrefix(value, "\ufeff")
	if start := strings.Index(value, "<?xml"); start >= 0 {
		value = value[start:]
	}
	value = strings.Replace(value, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	value = strings.Replace(value, `encoding="utf-16"`, `encoding="UTF-8"`, 1)
	return value
}

func restoreTaskXML(name, definition string) error {
	file, err := os.CreateTemp("", "task-scheduler-restore-*.xml")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err = file.WriteString(normalizeTaskXMLText(definition)); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	out, err := runSchtasks("/create", "/tn", name, "/xml", path, "/f")
	if err != nil {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

func updateTaskMetadata(name, description, startIn string) error {
	shortName, folderName := splitTaskPath(name)
	encode := func(value string) string {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
function Decode([string]$value) {
  [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($value))
}
$service = New-Object -ComObject Schedule.Service
$service.Connect()
$folder = $service.GetFolder((Decode '%s'))
$taskName = Decode '%s'
$definition = $folder.GetTask($taskName).Definition
$definition.RegistrationInfo.Description = Decode '%s'
if ($definition.Actions.Count -ne 1 -or [int]$definition.Actions.Item(1).Type -ne 0) {
  throw '任务操作不是单一程序操作。'
}
$definition.Actions.Item(1).WorkingDirectory = Decode '%s'
$userId = [string]$definition.Principal.UserId
$logonType = [int]$definition.Principal.LogonType
[void]$folder.RegisterTaskDefinition($taskName, $definition, 6, $userId, $null, $logonType)
`, encode(folderName), encode(shortName), encode(description), encode(startIn))
	out, err := runPowerShell(script)
	if err != nil {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// queryTasks 读取本机任务计划程序。
// Server 2008 R2 上 /v 详细查询偶尔会返回“无法加载到资源”，所以这里做多级回退。
func queryTasks() ([]Task, error) {
	attempts := []struct {
		name   string
		args   []string
		parser func(string) []Task
	}{
		{"LIST verbose", []string{"/query", "/fo", "LIST", "/v"}, parseTaskListOutput},
		{"CSV verbose", []string{"/query", "/fo", "CSV", "/v"}, parseTaskCSVOutput},
		{"LIST basic", []string{"/query", "/fo", "LIST"}, parseTaskListOutput},
		{"CSV basic", []string{"/query", "/fo", "CSV"}, parseTaskCSVOutput},
	}
	var lastErr string
	for _, attempt := range attempts {
		out, err := runSchtasks(attempt.args...)
		if err != nil {
			lastErr = strings.TrimSpace(out)
			debugLog("%s query failed: %s", attempt.name, lastErr)
			continue
		}
		tasks := mergeTasksByName(attempt.parser(out))
		debugLog("%s parsed %d tasks, output length=%d, head=%q", attempt.name, len(tasks), len(out), head(out, 500))
		if len(tasks) > 0 {
			sort.Slice(tasks, func(i, j int) bool {
				return strings.ToLower(tasks[i].Name) < strings.ToLower(tasks[j].Name)
			})
			return tasks, nil
		}
		lastErr = attempt.name + " returned no parseable tasks"
	}
	tasks, err := queryTasksViaPowerShell()
	if err == nil && len(tasks) > 0 {
		debugLog("PowerShell COM fallback parsed %d tasks", len(tasks))
		sortTasks(tasks)
		return tasks, nil
	}
	if err != nil {
		debugLog("PowerShell COM fallback failed: %v", err)
		lastErr = fmt.Sprintf("schtasks失败: %s；PowerShell COM失败: %v", lastErr, err)
	}
	if lastErr == "" {
		lastErr = "未读取到任务。"
	}
	return nil, errors.New(lastErr)
}

func sortTasks(tasks []Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return strings.ToLower(tasks[i].Name) < strings.ToLower(tasks[j].Name)
	})
}

// mergeTasksByName 将 schtasks /v 为同一任务的多个触发器返回的多行记录合并为一条。
// 任务路径在本机任务计划程序中唯一；不同记录中的计划类型会合并展示。
func mergeTasksByName(tasks []Task) []Task {
	merged := make([]Task, 0, len(tasks))
	indexes := make(map[string]int, len(tasks))
	for _, task := range tasks {
		key := strings.ToLower(strings.TrimSpace(task.Name))
		if key == "" {
			continue
		}
		if index, ok := indexes[key]; ok {
			existing := &merged[index]
			existing.ScheduleType = mergeTextValues(existing.ScheduleType, task.ScheduleType)
			if existing.NextRunTime == "" {
				existing.NextRunTime = task.NextRunTime
			}
			if existing.LastRunTime == "" {
				existing.LastRunTime = task.LastRunTime
			}
			if existing.TaskToRun == "" {
				existing.TaskToRun = task.TaskToRun
			}
			if existing.Comment == "" {
				existing.Comment = task.Comment
			}
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, task)
	}
	return merged
}

func mergeTextValues(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" || strings.EqualFold(left, right) {
		return left
	}
	for _, value := range strings.Split(left, ",") {
		if strings.EqualFold(strings.TrimSpace(value), right) {
			return left
		}
	}
	return left + ", " + right
}

// queryTasksViaPowerShell 通过 Task Scheduler COM 接口读取任务。
// 这是 schtasks.exe 资源文件损坏或语言资源加载失败时的兜底方案。
func queryTasksViaPowerShell() ([]Task, error) {
	script := `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
function B64($v) {
  if ($null -eq $v) { $v = '' }
  [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes([string]$v))
}
function Emit($values) {
  $encoded = @()
  foreach ($value in $values) { $encoded += (B64 $value) }
  [Console]::Out.WriteLine(($encoded -join "` + "`t" + `"))
}
function StateText($state) {
  switch ([int]$state) {
    0 { 'Unknown' }
    1 { 'Disabled' }
    2 { 'Queued' }
    3 { 'Ready' }
    4 { 'Running' }
    default { [string]$state }
  }
}
function TriggerText($type) {
  switch ([int]$type) {
    1 { '一次' }
    2 { '每天' }
    3 { '每周' }
    4 { '每月' }
    5 { '每月-星期' }
    6 { '空闲时' }
    7 { '注册时' }
    8 { '启动时' }
    9 { '登录时' }
    11 { '事件触发' }
    default { [string]$type }
  }
}
function DateText($value) {
  if ($null -eq $value) { return '' }
  try {
    $dt = [DateTime]$value
    if ($dt.Year -lt 1901) { return '' }
    return $dt.ToString('yyyy/M/d H:mm:ss')
  } catch {
    return [string]$value
  }
}
function QuotePath($path) {
  $p = [string]$path
  if ($p -match '\s' -and -not $p.StartsWith('"')) { return '"' + $p + '"' }
  return $p
}
function EmitTask($task) {
  $name = ''
  $status = ''
  $next = ''
  $last = ''
  $result = ''
  $schedule = ''
  $command = ''
  $startIn = ''
  $author = ''
  $comment = ''
  $enabled = 'false'
  try { $name = [string]$task.Path } catch {}
  try { $status = StateText $task.State } catch {}
  try { $next = DateText $task.NextRunTime } catch {}
  try { $last = DateText $task.LastRunTime } catch {}
  try { $result = [string]$task.LastTaskResult } catch {}
  try { if ($task.Enabled) { $enabled = 'true' } } catch {}
  $def = $null
  try { $def = $task.Definition } catch {}
  if ($null -ne $def) {
    try { $author = [string]$def.RegistrationInfo.Author } catch {}
    try { $comment = [string]$def.RegistrationInfo.Description } catch {}
    try {
      $triggers = @()
      foreach ($trigger in $def.Triggers) { $triggers += (TriggerText $trigger.Type) }
      $schedule = $triggers -join ','
    } catch {}
    try {
      foreach ($action in $def.Actions) {
        if ($command -eq '') {
          $path = ''
          $args = ''
          try { $path = [string]$action.Path } catch {}
          try { $args = [string]$action.Arguments } catch {}
          if ($path -ne '') {
            $command = QuotePath $path
            if ($args -ne '') { $command = $command + ' ' + $args }
          }
        }
        if ($startIn -eq '') {
          try { $startIn = [string]$action.WorkingDirectory } catch {}
        }
      }
    } catch {}
  }
  Emit @($name,$status,$next,$last,$result,$schedule,$command,$startIn,$author,$comment,$enabled)
}
function Walk($folder) {
  try {
    foreach ($task in $folder.GetTasks(0)) { EmitTask $task }
  } catch {}
  try {
    foreach ($sub in $folder.GetFolders(0)) { Walk $sub }
  } catch {}
}
$service = New-Object -ComObject Schedule.Service
$service.Connect()
Walk $service.GetFolder('\')
`
	out, err := runPowerShell(script)
	if err != nil {
		return nil, errors.New(strings.TrimSpace(out))
	}
	tasks := parsePowerShellTaskOutput(out)
	if len(tasks) == 0 {
		return nil, errors.New("PowerShell COM 未返回可解析任务")
	}
	return tasks, nil
}

func parsePowerShellTaskOutput(out string) []Task {
	var tasks []Task
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 11 {
			continue
		}
		fields := make([]string, len(parts))
		ok := true
		for i, part := range parts {
			value, err := decodeBase64Field(part)
			if err != nil {
				ok = false
				break
			}
			fields[i] = value
		}
		if !ok {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		shortName, location := splitTaskPath(name)
		tasks = append(tasks, Task{
			Name:         name,
			ShortName:    shortName,
			Location:     location,
			Status:       fields[1],
			NextRunTime:  normalizeTaskRunTime(fields[2]),
			LastRunTime:  normalizeTaskRunTime(fields[3]),
			LastResult:   fields[4],
			ScheduleType: fields[5],
			TaskToRun:    fields[6],
			StartIn:      fields[7],
			Author:       fields[8],
			Comment:      fields[9],
			Enabled:      strings.EqualFold(fields[10], "true"),
		})
	}
	return tasks
}

func decodeBase64Field(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizeTaskRunTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "N/A") || value == "从不" || value == "从未" {
		return "N/A"
	}
	datePart := strings.TrimSpace(strings.Split(value, " ")[0])
	datePart = strings.ReplaceAll(datePart, "-", "/")
	pieces := strings.Split(datePart, "/")
	if len(pieces) != 3 {
		return value
	}
	year, err := strconv.Atoi(strings.TrimSpace(pieces[0]))
	if err != nil {
		return value
	}
	month, err := strconv.Atoi(strings.TrimSpace(pieces[1]))
	if err != nil {
		return value
	}
	day, err := strconv.Atoi(strings.TrimSpace(pieces[2]))
	if err != nil {
		return value
	}
	if year <= 1901 || year == 1999 || year == 1601 || (year == 1899 && month == 12 && day == 30) {
		return "N/A"
	}
	return value
}

// parseTaskCSVOutput 解析 schtasks /fo csv /v 输出，作为 LIST 解析失败时的兜底。
func parseTaskCSVOutput(out string) []Task {
	reader := csv.NewReader(bytes.NewReader([]byte(out)))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		debugLog("CSV parse failed: %v", err)
		return nil
	}
	if len(records) < 2 {
		return nil
	}
	header := map[string]int{}
	for i, h := range records[0] {
		header[normalizeHeader(h)] = i
	}
	tasks := make([]Task, 0, len(records)-1)
	for _, row := range records[1:] {
		name := csvCell(row, header, "taskname", "任务名")
		if name == "" {
			continue
		}
		status := csvCell(row, header, "status", "状态", "模式")
		shortName, location := splitTaskPath(name)
		tasks = append(tasks, Task{
			Name:         name,
			ShortName:    shortName,
			Location:     location,
			Status:       status,
			NextRunTime:  normalizeTaskRunTime(csvCell(row, header, "nextruntime", "下次运行时间")),
			LastRunTime:  normalizeTaskRunTime(csvCell(row, header, "lastruntime", "上次运行时间")),
			LastResult:   csvCell(row, header, "lastresult", "上次结果"),
			ScheduleType: csvCell(row, header, "scheduletype", "计划类型"),
			TaskToRun:    csvCell(row, header, "tasktorun", "要运行的任务"),
			StartIn:      csvCell(row, header, "startin", "起始于", "起始目录"),
			Author:       csvCell(row, header, "author", "作者"),
			Comment:      csvCell(row, header, "comment", "注释", "描述"),
			Enabled:      !isDisabledStatus(status),
		})
	}
	return tasks
}

func csvCell(row []string, header map[string]int, keys ...string) string {
	for _, key := range keys {
		if i, ok := header[normalizeHeader(key)]; ok && i >= 0 && i < len(row) {
			return strings.TrimSpace(row[i])
		}
	}
	return ""
}

// parseTaskListOutput 解析 schtasks /fo LIST /v 输出。
// 中文 Windows 的字段名会随系统语言变化，所以解析前会统一 normalizeHeader。
func parseTaskListOutput(out string) []Task {
	var tasks []Task
	current := map[string]string{}
	flush := func() {
		name := firstField(current, "taskname", "任务名")
		if name == "" {
			current = map[string]string{}
			return
		}
		status := firstField(current, "status", "状态", "模式")
		shortName, location := splitTaskPath(name)
		tasks = append(tasks, Task{
			Name:         name,
			ShortName:    shortName,
			Location:     location,
			Status:       status,
			NextRunTime:  normalizeTaskRunTime(firstField(current, "nextruntime", "下次运行时间")),
			LastRunTime:  normalizeTaskRunTime(firstField(current, "lastruntime", "上次运行时间")),
			LastResult:   firstField(current, "lastresult", "上次结果"),
			ScheduleType: firstField(current, "scheduletype", "计划类型"),
			TaskToRun:    firstField(current, "tasktorun", "要运行的任务"),
			StartIn:      firstField(current, "startin", "起始于", "起始目录"),
			Author:       firstField(current, "author", "作者"),
			Comment:      firstField(current, "comment", "注释", "描述"),
			Enabled:      !isDisabledStatus(status),
		})
		current = map[string]string{}
	}

	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "Folder:") || strings.HasPrefix(line, "文件夹:") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := normalizeHeader(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		current[key] = value
	}
	flush()
	return tasks
}

// firstField 按多个候选字段名取第一个非空值，用来兼容中英文表头。
func firstField(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fields[normalizeHeader(key)]); value != "" {
			return value
		}
	}
	return ""
}

func isDisabledStatus(status string) bool {
	status = strings.TrimSpace(status)
	return strings.EqualFold(status, "Disabled") || status == "已禁用" || status == "禁用"
}

// splitTaskPath 将 \Folder\TaskName 拆成短任务名和任务计划程序位置。
func splitTaskPath(name string) (shortName string, location string) {
	name = strings.ReplaceAll(name, "/", `\`)
	if !strings.HasPrefix(name, `\`) {
		name = `\` + name
	}
	idx := strings.LastIndex(name, `\`)
	if idx <= 0 {
		return strings.TrimPrefix(name, `\`), `\`
	}
	shortName = name[idx+1:]
	location = name[:idx]
	if location == "" {
		location = `\`
	}
	return shortName, location
}

func actionArgs(action, name string) ([]string, error) {
	switch action {
	case "run":
		return []string{"/run", "/tn", name}, nil
	case "end":
		return []string{"/end", "/tn", name}, nil
	case "enable":
		return []string{"/change", "/tn", name, "/enable"}, nil
	case "disable":
		return []string{"/change", "/tn", name, "/disable"}, nil
	case "delete":
		return []string{"/delete", "/tn", name, "/f"}, nil
	default:
		return nil, errors.New("unsupported action")
	}
}

// runSchtasks 调用系统 schtasks.exe，并隐藏命令行窗口。
func runSchtasks(args ...string) (string, error) {
	cmd := exec.Command("schtasks.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return runCommandWithTimeout(cmd, externalCommandTimeout)
}

func runPowerShell(script string) (string, error) {
	return runPowerShellWithTimeout(script, externalCommandTimeout)
}

func runPowerShellWithTimeout(script string, timeout time.Duration) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", utf16LEBase64(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return runCommandWithTimeout(cmd, timeout)
}

func runCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		out, err := cmd.CombinedOutput()
		return decodeANSI(out), err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return decodeANSI(append(stdout.Bytes(), stderr.Bytes()...)), err
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			killProcessTree(cmd.Process.Pid)
		}
		err := <-done
		if err == nil {
			err = fmt.Errorf("外部命令执行超时（%s）：%s", timeout.Round(time.Second), strings.Join(cmd.Args, " "))
		}
		return decodeANSI(append(stdout.Bytes(), stderr.Bytes()...)), fmt.Errorf("外部命令执行超时（%s）：%s", timeout.Round(time.Second), strings.Join(cmd.Args, " "))
	}
}

func killProcessTree(pid int) {
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

func utf16LEBase64(s string) string {
	chars := syscall.StringToUTF16(s)
	if len(chars) > 0 && chars[len(chars)-1] == 0 {
		chars = chars[:len(chars)-1]
	}
	data := make([]byte, len(chars)*2)
	for i, ch := range chars {
		data[i*2] = byte(ch)
		data[i*2+1] = byte(ch >> 8)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// decodeANSI 使用当前系统代码页解码 schtasks 输出，解决中文 Windows 下的乱码解析问题。
func decodeANSI(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		return decodeUTF16Bytes(data[2:], false)
	}
	if len(data) >= 2 && data[0] == 0xfe && data[1] == 0xff {
		return decodeUTF16Bytes(data[2:], true)
	}
	// 新版 schtasks/PowerShell 可能直接输出 UTF-8；先识别 UTF-8，避免再次按系统
	// ANSI 代码页转换后出现“浠诲姟”一类乱码。Server 2008 R2 的本地代码页
	// 输出通常不是合法 UTF-8，会继续走下面的 MultiByteToWideChar 分支。
	if utf8.Valid(data) {
		return strings.TrimPrefix(string(data), "\ufeff")
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	multiByteToWideChar := kernel32.NewProc("MultiByteToWideChar")
	const cpACP = 0
	n, _, _ := multiByteToWideChar.Call(
		uintptr(cpACP),
		0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		0,
	)
	if n == 0 {
		return string(data)
	}
	wide := make([]uint16, n)
	ret, _, _ := multiByteToWideChar.Call(
		uintptr(cpACP),
		0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&wide[0])),
		n,
	)
	if ret == 0 {
		return string(data)
	}
	return syscall.UTF16ToString(wide)
}

func decodeUTF16Bytes(data []byte, bigEndian bool) string {
	values := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		var value uint16
		if bigEndian {
			value = uint16(data[index])<<8 | uint16(data[index+1])
		} else {
			value = uint16(data[index]) | uint16(data[index+1])<<8
		}
		values = append(values, value)
	}
	return strings.TrimPrefix(string(unicodeutf16.Decode(values)), "\ufeff")
}

// selfTestScheduler 逐一创建、查询并删除所有受支持计划类型的临时任务。
func selfTestScheduler() error {
	future := time.Now().AddDate(0, 0, 2)
	date := future.Format("2006/01/02")
	weekCodes := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	stamp := time.Now().Format("20060102_150405")
	tests := []struct {
		label  string
		config TaskCreateConfig
	}{
		{"一次", TaskCreateConfig{Schedule: "一次", StartDate: date, StartTime: "09:05"}},
		{"每天", TaskCreateConfig{Schedule: "每天", StartDate: date, StartTime: "09:10"}},
		{"每周", TaskCreateConfig{Schedule: "每周", StartDate: date, StartTime: "09:15", WeekDays: []string{weekCodes[int(future.Weekday())]}}},
		{"每月", TaskCreateConfig{Schedule: "每月", StartDate: date, StartTime: "09:20", MonthDay: strconv.Itoa(future.Day())}},
		{"每月最后一天", TaskCreateConfig{Schedule: "每月", StartDate: date, StartTime: "09:22", MonthDay: "LASTDAY"}},
		{"每天重复间隔", TaskCreateConfig{Schedule: "每天", StartDate: date, StartTime: "09:24", RepeatIntervalMinutes: 3, RepeatDurationMinutes: 60}},
		{"关机事件", TaskCreateConfig{Schedule: "关机时"}},
	}

	created := make([]string, 0, len(tests))
	defer func() {
		for _, name := range created {
			_, _ = runSchtasks("/delete", "/tn", name, "/f")
		}
	}()

	elevated := isElevated()
	if !elevated {
		fmt.Println("SELF TEST NOTICE: 当前进程未提升权限，使用当前用户验证计划语法；SYSTEM 权限请以管理员身份再次自测。")
	}
	for index := range tests {
		test := &tests[index]
		test.config.Name = fmt.Sprintf(`\CodexScheduleSelfTest_%s_%02d`, stamp, index+1)
		test.config.Program = "cmd.exe"
		test.config.Arguments = "/c exit 0"
		args, err := buildCreateArgs(test.config)
		if err != nil {
			return fmt.Errorf("%s 参数生成失败: %w", test.label, err)
		}
		if !elevated {
			args = removeSystemRunOptions(args)
		}
		out, err := runSchtasks(args...)
		if err != nil {
			return fmt.Errorf("%s 创建失败: %s", test.label, strings.TrimSpace(out))
		}
		created = append(created, test.config.Name)
		if out, err = runSchtasks("/query", "/tn", test.config.Name, "/fo", "LIST", "/v"); err != nil {
			return fmt.Errorf("%s 查询失败: %s", test.label, strings.TrimSpace(out))
		}
		testDescription := "Codex 自测：" + test.label
		testStartIn := filepath.Join(os.Getenv("SystemRoot"), "System32")
		if metadataErr := updateTaskMetadata(test.config.Name, testDescription, testStartIn); metadataErr != nil {
			return fmt.Errorf("%s 元数据修改失败: %v", test.label, metadataErr)
		}
		definition, exportErr := exportTaskXML(test.config.Name)
		if exportErr != nil {
			return fmt.Errorf("%s XML 导出失败: %v", test.label, exportErr)
		}
		if elevated {
			if err = validateSystemPrincipalXML(definition); err != nil {
				return fmt.Errorf("%s SYSTEM 权限验证失败: %v", test.label, err)
			}
		}
		parsedConfig, parseErr := parseTaskXMLConfig(test.config.Name, definition)
		if parseErr != nil {
			return fmt.Errorf("%s XML 回读失败: %v", test.label, parseErr)
		}
		if scheduleCode(parsedConfig.Schedule) != scheduleCode(test.config.Schedule) {
			return fmt.Errorf("%s XML 回读计划类型不一致: %s", test.label, parsedConfig.Schedule)
		}
		if scheduleCode(test.config.Schedule) == "MONTHLY" && strings.EqualFold(test.config.MonthDay, "LASTDAY") && !strings.EqualFold(parsedConfig.MonthDay, "LASTDAY") {
			return fmt.Errorf("%s XML 回读每月最后一天不一致: %s", test.label, parsedConfig.MonthDay)
		}
		if test.config.RepeatIntervalMinutes > 0 && (parsedConfig.RepeatIntervalMinutes != test.config.RepeatIntervalMinutes || parsedConfig.RepeatDurationMinutes != test.config.RepeatDurationMinutes) {
			return fmt.Errorf("%s XML 回读重复间隔不一致: %d/%d", test.label, parsedConfig.RepeatIntervalMinutes, parsedConfig.RepeatDurationMinutes)
		}
		if parsedConfig.Description != testDescription || !strings.EqualFold(parsedConfig.StartIn, testStartIn) {
			return fmt.Errorf("%s 描述或起始目录回读不一致", test.label)
		}
		if out, err = runSchtasks("/delete", "/tn", test.config.Name, "/f"); err != nil {
			return fmt.Errorf("%s 删除失败: %s", test.label, strings.TrimSpace(out))
		}
		created = created[:len(created)-1]
		fmt.Printf("[PASS] %s：创建、修改、XML 回读、删除成功\n", test.label)
	}
	renameOld := `\CodexScheduleSelfTest_` + stamp + `_RenameOld`
	renameNew := `\CodexScheduleSelfTest_` + stamp + `_RenameNew`
	defer func() {
		_, _ = runSchtasks("/delete", "/tn", renameOld, "/f")
		_, _ = runSchtasks("/delete", "/tn", renameNew, "/f")
	}()
	renameConfig := TaskCreateConfig{
		Name: renameOld, Description: "重命名前", Schedule: "每天", StartDate: date, StartTime: "09:30",
		Program: "cmd.exe", Arguments: "/c exit 0", StartIn: filepath.Join(os.Getenv("SystemRoot"), "System32"),
	}
	if err := saveTaskConfig(renameConfig, "", elevated); err != nil {
		return fmt.Errorf("重命名自测创建失败: %v", err)
	}
	renameConfig.Name = renameNew
	renameConfig.Description = "重命名后"
	if err := saveTaskConfig(renameConfig, renameOld, elevated); err != nil {
		return fmt.Errorf("重命名自测保存失败: %v", err)
	}
	if taskExists(renameOld) || !taskExists(renameNew) {
		return errors.New("重命名自测结果不一致")
	}
	renamedXML, err := exportTaskXML(renameNew)
	if err != nil {
		return fmt.Errorf("重命名自测 XML 导出失败: %v", err)
	}
	renamedConfig, err := parseTaskXMLConfig(renameNew, renamedXML)
	if err != nil || renamedConfig.Description != "重命名后" {
		return errors.New("重命名后的任务元数据回读失败")
	}
	if out, err := runSchtasks("/delete", "/tn", renameNew, "/f"); err != nil {
		return fmt.Errorf("重命名自测清理失败: %s", strings.TrimSpace(out))
	}
	fmt.Println("[PASS] 任务重命名：创建新名称、验证、删除旧名称成功")
	return nil
}

func validateSystemPrincipalXML(xmlText string) error {
	xmlText = normalizeTaskXMLText(xmlText)
	var definition schedulerTaskXML
	if err := xml.Unmarshal([]byte(xmlText), &definition); err != nil {
		return err
	}
	if len(definition.Principals.Principal) == 0 {
		return errors.New("任务 XML 中没有 Principal")
	}
	principal := definition.Principals.Principal[0]
	user := strings.ToUpper(strings.TrimSpace(principal.UserID))
	runLevel := strings.ToUpper(strings.TrimSpace(principal.RunLevel))
	if user != "SYSTEM" && user != "NT AUTHORITY\\SYSTEM" && user != "S-1-5-18" {
		return fmt.Errorf("运行账户不是 SYSTEM: %s", principal.UserID)
	}
	if runLevel != "" && runLevel != "HIGHESTAVAILABLE" {
		return fmt.Errorf("运行级别不是最高权限: %s", principal.RunLevel)
	}
	return nil
}

func removeSystemRunOptions(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if (strings.EqualFold(args[index], "/ru") || strings.EqualFold(args[index], "/rl")) && index+1 < len(args) {
			index++
			continue
		}
		result = append(result, args[index])
	}
	return result
}

func initCommonControls() {
	icc := win.INITCOMMONCONTROLSEX{
		DwSize: uint32(unsafe.Sizeof(win.INITCOMMONCONTROLSEX{})),
		DwICC:  win.ICC_LISTVIEW_CLASSES | win.ICC_STANDARD_CLASSES,
	}
	win.InitCommonControlsEx(&icc)
}

func initFonts() {
	// Server 2008 R2 通常没有 Microsoft YaHei UI，优先使用该系统自带的 Microsoft YaHei。
	uiFont = createFont("Microsoft YaHei", 9, 400)
	if uiFont == 0 {
		uiFont = createFont("Tahoma", 9, 400)
	}
	titleFont = createFont("Microsoft YaHei", 14, win.FW_BOLD)
	if titleFont == 0 {
		titleFont = createFont("Tahoma", 14, win.FW_BOLD)
	}
	sectionFont = createFont("Microsoft YaHei", 9, win.FW_BOLD)
	if sectionFont == 0 {
		sectionFont = createFont("Tahoma", 9, win.FW_BOLD)
	}
}

func initBrushes() {
	pageBrush = makeBrush(rgb(244, 246, 248), win.COLOR_BTNFACE)
	contentBrush = makeBrush(rgb(255, 255, 255), win.COLOR_WINDOW)
	headerBrush = makeBrush(rgb(234, 242, 255), win.COLOR_BTNFACE)
	borderBrush = makeBrush(rgb(203, 213, 225), win.COLOR_WINDOWFRAME)
	pressedBrush = makeBrush(rgb(226, 232, 240), win.COLOR_BTNFACE)
	primaryBrush = makeBrush(rgb(37, 99, 235), win.COLOR_HIGHLIGHT)
	primaryPressedBrush = makeBrush(rgb(29, 78, 216), win.COLOR_HIGHLIGHT)
}

func makeBrush(color win.COLORREF, fallback int) win.HBRUSH {
	brush := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: color})
	if brush != 0 {
		customBrushes = append(customBrushes, brush)
		return brush
	}
	return win.GetSysColorBrush(fallback)
}

func controlColor(wParam, lParam uintptr, staticControl bool) uintptr {
	hdc := win.HDC(wParam)
	hwnd := win.HWND(lParam)
	brush := contentBrush
	background := rgb(255, 255, 255)
	textColor := rgb(15, 23, 42)
	if hwnd == headerBand || hwnd == titleLabel || hwnd == subtitleLabel || hwnd == permissionLabel {
		brush = headerBrush
		background = rgb(234, 242, 255)
		if hwnd == subtitleLabel {
			textColor = rgb(71, 85, 105)
		}
		if hwnd == permissionLabel {
			textColor = rgb(30, 64, 175)
		}
	} else if staticControl && hwnd != detailEdit {
		brush = pageBrush
		background = rgb(244, 246, 248)
	}
	win.SetTextColor(hdc, textColor)
	win.SetBkColor(hdc, background)
	return uintptr(brush)
}

func createFont(face string, pointSize int32, weight int32) win.HFONT {
	var lf win.LOGFONT
	lf.LfHeight = -pointSize * 96 / 72
	lf.LfWeight = weight
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfOutPrecision = win.OUT_DEFAULT_PRECIS
	lf.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
	lf.LfQuality = win.CLEARTYPE_QUALITY
	copy(lf.LfFaceName[:], syscall.StringToUTF16(face))
	return win.CreateFontIndirect(&lf)
}

func createWindow(className interface{}, text string, style uint32, x, y, w, h int32, parent win.HWND, id uint16) win.HWND {
	return createWindowEx(0, className, text, style, x, y, w, h, parent, id)
}

func createWindowEx(exStyle uint32, className interface{}, text string, style uint32, x, y, w, h int32, parent win.HWND, id uint16) win.HWND {
	hwnd := win.CreateWindowEx(exStyle, classPtr(className), utf16(text), style, x, y, w, h, parent, win.HMENU(uintptr(id)), hInst, nil)
	setControlFont(hwnd, uiFont)
	return hwnd
}

func createLabel(parent win.HWND, text string, x, y, w, h int32) win.HWND {
	return createWindow("STATIC", text, win.WS_CHILD|win.WS_VISIBLE, x, y, w, h, parent, 0)
}

func createButton(parent win.HWND, text string, x, y, w, h int32, id uint16) win.HWND {
	return createWindow("BUTTON", text, win.WS_CHILD|win.WS_VISIBLE|win.BS_OWNERDRAW|win.WS_TABSTOP, x, y, w, h, parent, id)
}

func drawOwnerButton(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	draw := (*win.DRAWITEMSTRUCT)(unsafe.Pointer(lParam))
	if draw.CtlType != ownerDrawButton {
		return false
	}

	fill := contentBrush
	border := borderBrush
	textColor := rgb(15, 23, 42)
	pressed := draw.ItemState&win.ODS_SELECTED != 0
	disabled := draw.ItemState&(win.ODS_DISABLED|win.ODS_GRAYED) != 0
	primary := draw.CtlID == idCreate || draw.CtlID == idWizardCreate

	if primary {
		fill = primaryBrush
		border = primaryBrush
		textColor = rgb(255, 255, 255)
		if pressed {
			fill = primaryPressedBrush
			border = primaryPressedBrush
		}
	} else if pressed {
		fill = pressedBrush
	}
	if draw.CtlID == idRun {
		textColor = rgb(22, 101, 52)
	}
	if draw.CtlID == idDisable {
		textColor = rgb(146, 64, 14)
	}
	if draw.CtlID == idDelete {
		textColor = rgb(185, 28, 28)
	}
	if disabled {
		fill = pageBrush
		border = borderBrush
		textColor = rgb(148, 163, 184)
	}

	drawRect := draw.RcItem
	fillRectangle(draw.HDC, drawRect, border)
	drawRect.Left++
	drawRect.Top++
	drawRect.Right--
	drawRect.Bottom--
	fillRectangle(draw.HDC, drawRect, fill)

	oldFont := win.SelectObject(draw.HDC, win.HGDIOBJ(uiFont))
	win.SetBkMode(draw.HDC, win.TRANSPARENT)
	win.SetTextColor(draw.HDC, textColor)
	textRect := drawRect
	if pressed {
		textRect.Top++
		textRect.Left++
	}
	text := windowText(draw.HwndItem)
	win.DrawTextEx(draw.HDC, utf16(text), -1, &textRect, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE, nil)
	if draw.ItemState&win.ODS_FOCUS != 0 && !disabled {
		focusRect := drawRect
		focusRect.Left += 3
		focusRect.Top += 3
		focusRect.Right -= 3
		focusRect.Bottom -= 3
		win.DrawFocusRect(draw.HDC, &focusRect)
	}
	win.SelectObject(draw.HDC, oldFont)
	return true
}

func fillRectangle(hdc win.HDC, rect win.RECT, brush win.HBRUSH) {
	oldBrush := win.SelectObject(hdc, win.HGDIOBJ(brush))
	oldPen := win.SelectObject(hdc, win.GetStockObject(win.NULL_PEN))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)
	win.SelectObject(hdc, oldPen)
	win.SelectObject(hdc, oldBrush)
}

func setControlFont(hwnd win.HWND, font win.HFONT) {
	if hwnd != 0 && font != 0 {
		win.SendMessage(hwnd, win.WM_SETFONT, uintptr(font), 1)
	}
}

func addColumn(index int, title string, width int32) {
	var col win.LVCOLUMN
	col.Mask = win.LVCF_TEXT | win.LVCF_WIDTH | win.LVCF_SUBITEM
	col.Cx = width
	col.ISubItem = int32(index)
	col.PszText = utf16(title)
	win.SendMessage(listView, win.LVM_INSERTCOLUMN, uintptr(index), uintptr(unsafe.Pointer(&col)))
}

func insertItem(index int, values []string) {
	var item win.LVITEM
	item.Mask = win.LVIF_TEXT
	item.IItem = int32(index)
	item.ISubItem = 0
	item.PszText = utf16(values[0])
	win.SendMessage(listView, win.LVM_INSERTITEM, 0, uintptr(unsafe.Pointer(&item)))
	for col := 1; col < len(values); col++ {
		item.ISubItem = int32(col)
		item.PszText = utf16(values[col])
		win.SendMessage(listView, win.LVM_SETITEM, 0, uintptr(unsafe.Pointer(&item)))
	}
}

func comboAdd(hwnd win.HWND, text string) {
	win.SendMessage(hwnd, win.CB_ADDSTRING, 0, uintptr(unsafe.Pointer(utf16(text))))
}

func comboText(hwnd win.HWND) string {
	index := win.SendMessage(hwnd, win.CB_GETCURSEL, 0, 0)
	if int32(index) < 0 {
		return ""
	}
	length := win.SendMessage(hwnd, win.CB_GETLBTEXTLEN, index, 0)
	if int32(length) < 0 {
		return ""
	}
	buf := make([]uint16, int(length)+1)
	win.SendMessage(hwnd, win.CB_GETLBTEXT, index, uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func windowText(hwnd win.HWND) string {
	n := int(win.SendMessage(hwnd, win.WM_GETTEXTLENGTH, 0, 0))
	buf := make([]uint16, n+1)
	win.SendMessage(hwnd, win.WM_GETTEXT, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func setWindowText(hwnd win.HWND, text string) {
	win.SendMessage(hwnd, win.WM_SETTEXT, 0, uintptr(unsafe.Pointer(utf16(text))))
}

func message(parent win.HWND, title, text string) {
	win.MessageBox(parent, utf16(text), utf16(title), win.MB_OK|win.MB_ICONINFORMATION)
}

func classPtr(value interface{}) *uint16 {
	switch v := value.(type) {
	case string:
		return utf16(v)
	case *uint16:
		return v
	default:
		return nil
	}
}

func utf16(s string) *uint16 {
	return syscall.StringToUTF16Ptr(s)
}

func rgb(r, g, b byte) win.COLORREF {
	return win.COLORREF(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

func isElevated() bool {
	ret, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin").Call()
	return ret != 0
}

// relaunchElevated 使用 runas 重新启动自身，让正式 GUI 获得管理员权限。
func relaunchElevated() bool {
	exe, err := os.Executable()
	if err != nil {
		debugLog("relaunchElevated failed to get executable: %v", err)
		return false
	}
	cwd, _ := os.Getwd()
	ret, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(
		0,
		uintptr(unsafe.Pointer(utf16("runas"))),
		uintptr(unsafe.Pointer(utf16(exe))),
		0,
		uintptr(unsafe.Pointer(utf16(cwd))),
		uintptr(win.SW_SHOWNORMAL),
	)
	debugLog("relaunchElevated ShellExecute return=%d", ret)
	return ret > 32
}

// wizardWndProc 是“新建任务”弹窗的消息处理函数。
func wizardWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_CREATE:
		createWizardControls(hwnd)
		return 0
	case win.WM_COMMAND:
		switch win.LOWORD(uint32(wParam)) {
		case idWizardBrowse:
			browseProgram(hwnd)
			return 0
		case idWizardBrowseDir:
			browseStartDir(hwnd)
			return 0
		case idWizardSched:
			if win.HIWORD(uint32(wParam)) == win.CBN_SELCHANGE {
				updateWizardScheduleOptions()
			}
			return 0
		case idWizardRepeat:
			updateRepeatControls()
			return 0
		case idWizardCreate:
			safeCreateBasicTask()
			return 0
		case idWizardCancel:
			if wizardSaving {
				message(hwnd, "正在保存", "任务正在保存，请稍候。")
				return 0
			}
			win.DestroyWindow(hwnd)
			return 0
		}
	case win.WM_DRAWITEM:
		if drawOwnerButton(lParam) {
			return 1
		}
		return 0
	case win.WM_CTLCOLORSTATIC:
		return controlColor(wParam, lParam, true)
	case win.WM_CTLCOLOREDIT:
		return controlColor(wParam, lParam, false)
	case win.WM_CLOSE:
		if wizardSaving {
			message(hwnd, "正在保存", "任务正在保存，请稍候。")
			return 0
		}
		win.DestroyWindow(hwnd)
		return 0
	case win.WM_DESTROY:
		wizardWnd = 0
		wizardInitialConfig = nil
		wizardOriginalName = ""
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

// createWizardControls 创建新建任务弹窗中的基础信息、触发器和执行动作控件。
func createWizardControls(hwnd win.HWND) {
	basicGroup := createWindow("BUTTON", "基本信息", win.WS_CHILD|win.WS_VISIBLE|win.BS_GROUPBOX, 18, 16, 748, 104, hwnd, 0)
	setControlFont(basicGroup, sectionFont)
	createLabel(hwnd, "任务名称", 34, 48, 76, 24)
	wizardName = createWindow("EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 116, 44, 626, 28, hwnd, idWizardName)
	win.SendMessage(wizardName, win.EM_SETCUEBANNER, 1, uintptr(unsafe.Pointer(utf16("请输入任务名称，选择执行文件不会改变此名称"))))
	createLabel(hwnd, "任务描述", 34, 84, 76, 24)
	wizardDesc = createWindow("EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 116, 80, 626, 28, hwnd, idWizardDesc)

	triggerGroup := createWindow("BUTTON", "触发器", win.WS_CHILD|win.WS_VISIBLE|win.BS_GROUPBOX, 18, 132, 748, 148, hwnd, 0)
	setControlFont(triggerGroup, sectionFont)
	createLabel(hwnd, "计划类型", 34, 165, 76, 24)
	wizardSched = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 116, 160, 150, 180, hwnd, idWizardSched)
	comboAdd(wizardSched, "每天")
	comboAdd(wizardSched, "一次")
	comboAdd(wizardSched, "每周")
	comboAdd(wizardSched, "每月")
	comboAdd(wizardSched, "关机时")
	win.SendMessage(wizardSched, win.CB_SETCURSEL, 0, 0)
	wizardDateLabel = createLabel(hwnd, "开始日期", 286, 165, 76, 24)
	wizardDate = createWindow("EDIT", time.Now().Format("2006/01/02"), win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 364, 160, 120, 28, hwnd, idWizardDate)
	wizardTimeLabel = createLabel(hwnd, "时间点", 504, 165, 58, 24)
	wizardTime = createWindow("EDIT", "09:00", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 564, 160, 100, 28, hwnd, idWizardTime)

	wizardWeekLabel = createLabel(hwnd, "每周日期", 34, 204, 76, 24)
	weekNames := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	wizardWeekChecks = wizardWeekChecks[:0]
	for i, name := range weekNames {
		cb := createWindow("BUTTON", name, win.WS_CHILD|win.WS_VISIBLE|win.BS_AUTOCHECKBOX, 116+int32(i)*82, 200, 76, 24, hwnd, uint16(idWizardWeekBase+i))
		wizardWeekChecks = append(wizardWeekChecks, cb)
	}
	win.SendMessage(wizardWeekChecks[1], win.BM_SETCHECK, win.BST_CHECKED, 0)
	wizardMonthLabel = createLabel(hwnd, "每月日期", 34, 204, 76, 24)
	wizardMonthDay = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 116, 199, 140, 240, hwnd, idWizardMonthDay)
	for i := 1; i <= 31; i++ {
		comboAdd(wizardMonthDay, fmt.Sprintf("%d 日", i))
	}
	comboAdd(wizardMonthDay, "最后一天")
	win.SendMessage(wizardMonthDay, win.CB_SETCURSEL, 0, 0)

	wizardRepeatCheck = createWindow("BUTTON", "重复执行", win.WS_CHILD|win.WS_VISIBLE|win.BS_AUTOCHECKBOX, 34, 238, 90, 24, hwnd, idWizardRepeat)
	createLabel(hwnd, "间隔", 136, 240, 40, 24)
	wizardRepeatInt = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 178, 235, 120, 180, hwnd, idWizardRepeatInt)
	for _, item := range []string{"1 分钟", "3 分钟", "5 分钟", "10 分钟", "15 分钟", "30 分钟", "1 小时"} {
		comboAdd(wizardRepeatInt, item)
	}
	win.SendMessage(wizardRepeatInt, win.CB_SETCURSEL, 6, 0)
	createLabel(hwnd, "持续时间", 318, 240, 68, 24)
	wizardRepeatDur = createWindow("COMBOBOX", "", win.WS_CHILD|win.WS_VISIBLE|win.CBS_DROPDOWNLIST|win.WS_VSCROLL, 390, 235, 120, 180, hwnd, idWizardRepeatDur)
	for _, item := range []string{"1 小时", "12 小时", "1 天", "3 天"} {
		comboAdd(wizardRepeatDur, item)
	}
	win.SendMessage(wizardRepeatDur, win.CB_SETCURSEL, 2, 0)

	actionGroup := createWindow("BUTTON", "执行操作", win.WS_CHILD|win.WS_VISIBLE|win.BS_GROUPBOX, 18, 294, 748, 138, hwnd, 0)
	setControlFont(actionGroup, sectionFont)
	createLabel(hwnd, "执行文件", 34, 327, 76, 24)
	wizardProgram = createWindow("EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 116, 322, 526, 28, hwnd, idWizardProg)
	createButton(hwnd, "浏览...", 652, 321, 90, 30, idWizardBrowse)
	createLabel(hwnd, "执行参数", 34, 365, 76, 24)
	wizardArgs = createWindow("EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 116, 360, 626, 28, hwnd, idWizardArgs)
	createLabel(hwnd, "起始目录", 34, 403, 76, 24)
	wizardStartIn = createWindow("EDIT", "", win.WS_CHILD|win.WS_VISIBLE|win.WS_BORDER|win.ES_AUTOHSCROLL, 116, 398, 526, 28, hwnd, idWizardStart)
	createButton(hwnd, "浏览...", 652, 397, 90, 30, idWizardBrowseDir)

	permissionNote := createLabel(hwnd, "权限：任务将以 NT AUTHORITY\\SYSTEM 账户和最高权限运行，无需保存用户密码。\r\n关机触发器会忽略开始日期、时间点和重复间隔。", 30, 450, 720, 48)
	setControlFont(permissionNote, sectionFont)
	wizardSubmitButton = createButton(hwnd, "创建任务", 560, 514, 96, 32, idWizardCreate)
	createButton(hwnd, "取消", 666, 514, 76, 32, idWizardCancel)
	if wizardInitialConfig != nil {
		populateWizard(*wizardInitialConfig)
		setWindowText(wizardSubmitButton, "保存修改")
	} else {
		updateWizardScheduleOptions()
	}
}

func populateWizard(config TaskCreateConfig) {
	name := strings.TrimSpace(config.Name)
	if strings.HasPrefix(name, `\`) && strings.Count(name, `\`) == 1 {
		name = strings.TrimPrefix(name, `\`)
	}
	setWindowText(wizardName, name)
	setWindowText(wizardDesc, config.Description)
	setWindowText(wizardProgram, config.Program)
	setWindowText(wizardArgs, config.Arguments)
	setWindowText(wizardStartIn, config.StartIn)
	if config.StartDate != "" {
		setWindowText(wizardDate, config.StartDate)
	}
	if config.StartTime != "" {
		setWindowText(wizardTime, config.StartTime)
	}
	scheduleIndex := map[string]int{"每天": 0, "一次": 1, "每周": 2, "每月": 3, "关机时": 4}[config.Schedule]
	win.SendMessage(wizardSched, win.CB_SETCURSEL, uintptr(scheduleIndex), 0)
	selectedDays := make(map[string]bool, len(config.WeekDays))
	for _, day := range config.WeekDays {
		selectedDays[strings.ToUpper(day)] = true
	}
	weekCodes := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	for index, checkbox := range wizardWeekChecks {
		state := uintptr(win.BST_UNCHECKED)
		if selectedDays[weekCodes[index]] {
			state = win.BST_CHECKED
		}
		win.SendMessage(checkbox, win.BM_SETCHECK, state, 0)
	}
	if config.MonthDay != "" {
		monthIndex := 0
		if strings.EqualFold(config.MonthDay, "LAST") || strings.EqualFold(config.MonthDay, "LASTDAY") {
			monthIndex = 31
		} else if day, err := strconv.Atoi(config.MonthDay); err == nil && day >= 1 && day <= 31 {
			monthIndex = day - 1
		}
		win.SendMessage(wizardMonthDay, win.CB_SETCURSEL, uintptr(monthIndex), 0)
	}
	if config.RepeatIntervalMinutes > 0 {
		win.SendMessage(wizardRepeatCheck, win.BM_SETCHECK, win.BST_CHECKED, 0)
		setRepeatComboByMinutes(wizardRepeatInt, []int{1, 3, 5, 10, 15, 30, 60}, config.RepeatIntervalMinutes)
		setRepeatComboByMinutes(wizardRepeatDur, []int{60, 720, 1440, 4320}, config.RepeatDurationMinutes)
	}
	updateWizardScheduleOptions()
}

func scheduleCode(text string) string {
	switch strings.TrimSpace(strings.ToUpper(text)) {
	case "每天", "每日", "DAILY":
		return "DAILY"
	case "一次", "单次", "ONCE":
		return "ONCE"
	case "每周", "WEEKLY":
		return "WEEKLY"
	case "每月", "MONTHLY":
		return "MONTHLY"
	case "关机时", "关机", "SHUTDOWN", "ONEVENT_SHUTDOWN":
		return "ONEVENT_SHUTDOWN"
	default:
		return text
	}
}

// updateWizardScheduleOptions 根据触发器类型显示每周或每月的专属配置。
func updateWizardScheduleOptions() {
	schedule := scheduleCode(comboText(wizardSched))
	showWeek := schedule == "WEEKLY"
	showMonth := schedule == "MONTHLY"
	showDateTime := schedule != "ONEVENT_SHUTDOWN"
	showControl(wizardDateLabel, showDateTime)
	showControl(wizardDate, showDateTime)
	showControl(wizardTimeLabel, showDateTime)
	showControl(wizardTime, showDateTime)
	showControl(wizardWeekLabel, showWeek)
	for _, hwnd := range wizardWeekChecks {
		showControl(hwnd, showWeek)
	}
	showControl(wizardMonthLabel, showMonth)
	showControl(wizardMonthDay, showMonth)
	if schedule == "ONEVENT_SHUTDOWN" {
		win.SendMessage(wizardRepeatCheck, win.BM_SETCHECK, win.BST_UNCHECKED, 0)
	}
	win.EnableWindow(wizardRepeatCheck, schedule != "ONEVENT_SHUTDOWN")
	updateRepeatControls()
}

func updateRepeatControls() {
	enabled := win.SendMessage(wizardRepeatCheck, win.BM_GETCHECK, 0, 0) == win.BST_CHECKED && win.IsWindowEnabled(wizardRepeatCheck)
	win.EnableWindow(wizardRepeatInt, enabled)
	win.EnableWindow(wizardRepeatDur, enabled)
}

func showControl(hwnd win.HWND, show bool) {
	if hwnd == 0 {
		return
	}
	if show {
		win.ShowWindow(hwnd, win.SW_SHOW)
	} else {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
}

// selectedWeekDays 将中文星期复选框转换为 schtasks 接受的 SUN/MON/TUE 等缩写。
func selectedWeekDays() []string {
	codes := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	var days []string
	for i, hwnd := range wizardWeekChecks {
		if win.SendMessage(hwnd, win.BM_GETCHECK, 0, 0) == win.BST_CHECKED {
			days = append(days, codes[i])
		}
	}
	return days
}

// selectedMonthDay 将“每月日期”下拉框转换为 schtasks 的 /d 参数。
func selectedMonthDay() string {
	text := comboText(wizardMonthDay)
	if strings.Contains(text, "最后") {
		return "LASTDAY"
	}
	fields := strings.Fields(text)
	if len(fields) > 0 {
		return fields[0]
	}
	return "1"
}

func selectedRepeatIntervalMinutes() int {
	return repeatMinutesFromText(comboText(wizardRepeatInt))
}

func selectedRepeatDurationMinutes() int {
	minutes := repeatMinutesFromText(comboText(wizardRepeatDur))
	if minutes <= 0 {
		return 1440
	}
	return minutes
}

func repeatMinutesFromText(text string) int {
	text = strings.TrimSpace(text)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	if strings.Contains(text, "天") {
		return value * 24 * 60
	}
	if strings.Contains(text, "小时") {
		return value * 60
	}
	return value
}

func setRepeatComboByMinutes(hwnd win.HWND, options []int, minutes int) {
	if hwnd == 0 || minutes <= 0 {
		return
	}
	for i, option := range options {
		if option == minutes {
			win.SendMessage(hwnd, win.CB_SETCURSEL, uintptr(i), 0)
			return
		}
	}
}

// normalizeTaskDate 将常见日期输入统一为 Server 2008 R2 schtasks 要求的 yyyy/MM/dd。
func normalizeTaskDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	layouts := []string{"2006/1/2", "2006-1-2", "2006/01/02", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t.Format("2006/01/02"), nil
		}
	}
	return "", errors.New("开始日期无效，请使用 yyyy/MM/dd 格式，例如 2026/07/06。")
}

func normalizeTaskTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "09:00"
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return "", errors.New("时间点无效，请使用 HH:mm 格式，例如 09:00 或 19:30。")
	}
	return parsed.Format("15:04"), nil
}

func normalizeWeekDays(values []string) ([]string, error) {
	valid := map[string]bool{"SUN": true, "MON": true, "TUE": true, "WED": true, "THU": true, "FRI": true, "SAT": true}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		day := strings.ToUpper(strings.TrimSpace(value))
		if !valid[day] {
			return nil, fmt.Errorf("无效的星期参数：%s。", value)
		}
		if !seen[day] {
			seen[day] = true
			result = append(result, day)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("每周任务至少要选择一个星期。")
	}
	return result, nil
}

func normalizeMonthDay(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "1", nil
	}
	if value == "LASTDAY" || value == "LAST" {
		return "LASTDAY", nil
	}
	if strings.Contains(value, "最后") {
		return "LASTDAY", nil
	}
	dayText := strings.Fields(value)
	if len(dayText) > 0 {
		value = dayText[0]
	}
	if strings.EqualFold(value, "LASTDAY") {
		return value, nil
	}
	day, err := strconv.Atoi(value)
	if err != nil || day < 1 || day > 31 {
		return "", errors.New("每月日期必须是 1 到 31，或最后一天。")
	}
	return strconv.Itoa(day), nil
}

func normalizeRepeatSettings(schedule string, intervalMinutes, durationMinutes int) (int, int, error) {
	if intervalMinutes <= 0 {
		return 0, 0, nil
	}
	if schedule == "ONEVENT_SHUTDOWN" {
		return 0, 0, errors.New("关机触发器不支持重复间隔。")
	}
	if intervalMinutes < 1 || intervalMinutes > 599940 {
		return 0, 0, errors.New("重复间隔必须在 1 到 599940 分钟之间。")
	}
	if durationMinutes <= 0 {
		durationMinutes = 1440
	}
	if durationMinutes < intervalMinutes {
		return 0, 0, errors.New("持续时间不能小于重复间隔。")
	}
	return intervalMinutes, durationMinutes, nil
}

func formatDurationForSchtasks(minutes int) string {
	if minutes <= 0 {
		minutes = 1440
	}
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func parseXMLRepetition(repetition *schedulerXMLRepetition) (int, int, error) {
	if repetition == nil {
		return 0, 0, nil
	}
	interval, err := parseISODurationMinutes(repetition.Interval)
	if err != nil {
		return 0, 0, fmt.Errorf("重复间隔无法识别：%w", err)
	}
	duration := 0
	if strings.TrimSpace(repetition.Duration) != "" {
		duration, err = parseISODurationMinutes(repetition.Duration)
		if err != nil {
			return 0, 0, fmt.Errorf("持续时间无法识别：%w", err)
		}
	}
	return interval, duration, nil
}

func parseISODurationMinutes(value string) (int, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0, errors.New("为空")
	}
	if !strings.HasPrefix(value, "P") {
		return 0, errors.New("不是 ISO 持续时间")
	}
	value = strings.TrimPrefix(value, "P")
	minutes := 0
	inTime := false
	number := ""
	for _, ch := range value {
		if ch >= '0' && ch <= '9' {
			number += string(ch)
			continue
		}
		if ch == 'T' {
			inTime = true
			continue
		}
		if number == "" {
			return 0, errors.New("缺少数字")
		}
		n, err := strconv.Atoi(number)
		if err != nil {
			return 0, err
		}
		number = ""
		switch ch {
		case 'D':
			minutes += n * 24 * 60
		case 'H':
			if !inTime {
				return 0, errors.New("小时必须位于时间部分")
			}
			minutes += n * 60
		case 'M':
			if !inTime {
				return 0, errors.New("月份持续时间不支持")
			}
			minutes += n
		default:
			return 0, fmt.Errorf("不支持的单位 %c", ch)
		}
	}
	if number != "" || minutes <= 0 {
		return 0, errors.New("格式不完整")
	}
	return minutes, nil
}

func browseProgram(parent win.HWND) {
	path := openFileDialog(parent)
	if path == "" {
		return
	}
	setWindowText(wizardProgram, path)
}

func browseStartDir(parent win.HWND) {
	dir := openFolderDialog(parent)
	if dir != "" {
		setWindowText(wizardStartIn, dir)
	}
}

// openFileDialog 通过 PowerShell/.NET 打开文件选择框，避免手写复杂 Win32 对话框代码。
func openFileDialog(parent win.HWND) string {
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$d = New-Object System.Windows.Forms.OpenFileDialog",
		"$d.Title = '选择程序或脚本'",
		"$d.Filter = '可执行和脚本文件 (*.exe;*.bat;*.cmd;*.ps1;*.vbs;*.js)|*.exe;*.bat;*.cmd;*.ps1;*.vbs;*.js|所有文件 (*.*)|*.*'",
		"$d.CheckFileExists = $true",
		"$d.CheckPathExists = $true",
		"if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [IO.File]::WriteAllText($env:CODEX_PICKER_OUT, $d.FileName, [System.Text.Encoding]::UTF8) }",
	}, "\r\n")
	return runExternalPicker(parent, script, "选择程序或脚本失败")
}

func openTaskImportDialog() string {
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$d = New-Object System.Windows.Forms.OpenFileDialog",
		"$d.Title = '选择任务备份包或 XML 文件'",
		"$d.Filter = '任务备份包或 XML (*.zip;*.xml)|*.zip;*.xml|任务备份包 (*.zip)|*.zip|任务 XML (*.xml)|*.xml'",
		"$d.CheckFileExists = $true",
		"$d.CheckPathExists = $true",
		"if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [IO.File]::WriteAllText($env:CODEX_PICKER_OUT, $d.FileName, [System.Text.Encoding]::UTF8) }",
	}, "\r\n")
	return runExternalPicker(mainWnd, script, "选择导入文件失败")
}

func saveTaskArchiveDialog(title, defaultName string) string {
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$d = New-Object System.Windows.Forms.SaveFileDialog",
		"$d.Title = '" + title + "'",
		"$d.Filter = '任务备份包 (*.zip)|*.zip'",
		"$d.DefaultExt = 'zip'",
		"$d.AddExtension = $true",
		"$d.OverwritePrompt = $true",
		"$d.FileName = '" + defaultName + "'",
		"if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [IO.File]::WriteAllText($env:CODEX_PICKER_OUT, $d.FileName, [System.Text.Encoding]::UTF8) }",
	}, "\r\n")
	return runExternalPicker(mainWnd, script, "选择保存位置失败")
}

func openFolderDialog(parent win.HWND) string {
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$d = New-Object System.Windows.Forms.FolderBrowserDialog",
		"$d.Description = '选择起始目录'",
		"$d.ShowNewFolderButton = $true",
		"if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [IO.File]::WriteAllText($env:CODEX_PICKER_OUT, $d.SelectedPath, [System.Text.Encoding]::UTF8) }",
	}, "\r\n")
	return runExternalPicker(parent, script, "选择起始目录失败")
}

func runExternalPicker(parent win.HWND, script string, title string) string {
	debugLog("runExternalPicker start: %s", title)
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("task-scheduler-picker-%d.txt", time.Now().UnixNano()))
	_ = os.Remove(tempPath)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "CODEX_PICKER_OUT="+tempPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	if err := cmd.Run(); err != nil {
		debugLog("external picker failed: %v", err)
		message(parent, title, "外部选择窗口打开失败，请确认 powershell.exe 可用。\n\n详情见 debug.log")
		return ""
	}
	data, err := os.ReadFile(tempPath)
	_ = os.Remove(tempPath)
	if err != nil {
		debugLog("external picker cancelled or no result: %v", err)
		return ""
	}
	result := strings.TrimPrefix(strings.TrimSpace(string(data)), "\ufeff")
	debugLog("external picker selected: %q", result)
	return result
}

func debugLog(format string, args ...interface{}) {
	line := time.Now().Format("2006-01-02 15:04:05.000 ") + fmt.Sprintf(format, args...) + "\r\n"
	f, err := os.OpenFile("debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func quoteIfNeeded(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
		return path
	}
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}

// normalizeHeader 去掉空格、制表符和 BOM，并转小写，便于匹配 schtasks 字段名。
func normalizeHeader(s string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\ufeff", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(s)))
}
