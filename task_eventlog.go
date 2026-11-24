package main

import (
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type TaskExecutionRecord struct {
	InstanceID string `json:"instanceId"`
	TaskName   string `json:"taskName"`
	Action     string `json:"action"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Duration   string `json:"duration"`
	ExitCode   string `json:"exitCode"`
	Status     string `json:"status"` // "成功", "失败", "运行中", "未知"
}

type wevtEventItem struct {
	System struct {
		EventID     int `xml:"EventID"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		Correlation struct {
			ActivityID string `xml:"ActivityID,attr"`
		} `xml:"Correlation"`
	} `xml:"System"`
	EventData struct {
		Data []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
}

type wevtEventContainer struct {
	XMLName xml.Name        `xml:"Events"`
	Events  []wevtEventItem `xml:"Event"`
}

func parseWevtutilXML(xmlText string) ([]wevtEventItem, error) {
	xmlText = strings.TrimSpace(xmlText)
	if xmlText == "" {
		return nil, nil
	}
	if !strings.HasPrefix(xmlText, "<Events") {
		xmlText = "<Events>" + xmlText + "</Events>"
	}
	var container wevtEventContainer
	if err := xml.Unmarshal([]byte(xmlText), &container); err != nil {
		return nil, fmt.Errorf("解析事件日志 XML 失败: %w", err)
	}
	return container.Events, nil
}

func getEventData(event wevtEventItem, key string) string {
	for _, d := range event.EventData.Data {
		if strings.EqualFold(d.Name, key) {
			return strings.TrimSpace(d.Value)
		}
	}
	return ""
}

func getEventInstanceID(event wevtEventItem) string {
	if id := getEventData(event, "InstanceId"); id != "" {
		return id
	}
	if id := getEventData(event, "TaskInstanceId"); id != "" {
		return id
	}
	if id := strings.TrimSpace(event.System.Correlation.ActivityID); id != "" {
		return id
	}
	return ""
}

func pairExecutionEvents(events []wevtEventItem, targetTaskName string) []TaskExecutionRecord {
	targetTaskName = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(targetTaskName)), `\`)

	type instanceGroup struct {
		startEvent       *wevtEventItem
		endEvent         *wevtEventItem
		actionStartEvent *wevtEventItem
		actionEvent      *wevtEventItem
		taskName         string
		action           string
		exitCode         string
	}

	groups := make(map[string]*instanceGroup)
	var orderedInstanceIDs []string

	for i := range events {
		ev := events[i]
		tName := getEventData(ev, "TaskName")
		normTName := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tName)), `\`)
		if targetTaskName != "" && normTName != targetTaskName && !strings.Contains(normTName, targetTaskName) {
			continue
		}
		instID := getEventInstanceID(ev)
		if instID == "" {
			instID = fmt.Sprintf("no-guid-%d", i)
		}

		g, exists := groups[instID]
		if !exists {
			g = &instanceGroup{taskName: tName}
			groups[instID] = g
			orderedInstanceIDs = append(orderedInstanceIDs, instID)
		}

		switch ev.System.EventID {
		case 100:
			g.startEvent = &ev
		case 102:
			g.endEvent = &ev
		case 201:
			g.actionEvent = &ev
			if code := getEventData(ev, "ResultCode"); code != "" {
				g.exitCode = code
			}
			if act := getEventData(ev, "ActionName"); act != "" {
				g.action = act
			}
		case 200:
			g.actionStartEvent = &ev
			if act := getEventData(ev, "ActionName"); act != "" && g.action == "" {
				g.action = act
			}
		case 322, 329:
			if g.exitCode == "" {
				g.exitCode = fmt.Sprintf("Event%d", ev.System.EventID)
			}
		}
	}

	var records []TaskExecutionRecord
	for _, id := range orderedInstanceIDs {
		g := groups[id]
		rec := TaskExecutionRecord{
			InstanceID: id,
			TaskName:   g.taskName,
			Action:     g.action,
			ExitCode:   g.exitCode,
		}
		var startTime, endTime time.Time
		startEv := g.startEvent
		if startEv == nil {
			startEv = g.actionStartEvent
		}
		if startEv != nil {
			t, _ := time.Parse(time.RFC3339Nano, startEv.System.TimeCreated.SystemTime)
			if !t.IsZero() {
				startTime = t.Local()
				rec.StartTime = startTime.Format("2006-01-02 15:04:05")
			}
		}
		endEv := g.endEvent
		if endEv == nil {
			endEv = g.actionEvent
		}
		if endEv != nil {
			t, _ := time.Parse(time.RFC3339Nano, endEv.System.TimeCreated.SystemTime)
			if !t.IsZero() {
				endTime = t.Local()
				rec.EndTime = endTime.Format("2006-01-02 15:04:05")
			}
		}
		if !startTime.IsZero() && !endTime.IsZero() && !endTime.Before(startTime) {
			duration := endTime.Sub(startTime)
			if duration < time.Second {
				rec.Duration = fmt.Sprintf("%dms", duration.Milliseconds())
			} else {
				rec.Duration = fmt.Sprintf("%.2fs", duration.Seconds())
			}
		} else if !startTime.IsZero() && endTime.IsZero() && rec.ExitCode == "" {
			rec.Duration = "运行中"
		} else {
			rec.Duration = "-"
		}

		if rec.ExitCode == "0" || rec.ExitCode == "0x0" {
			rec.Status = "成功"
		} else if rec.ExitCode != "" {
			rec.Status = "失败 (" + rec.ExitCode + ")"
		} else if endEv != nil {
			rec.Status = "完成"
		} else if startEv != nil {
			rec.Status = "运行中"
		} else {
			rec.Status = "未知"
		}
		records = append(records, rec)
	}
	return records
}

func parseChannelEnabledFromText(text string) bool {
	for _, line := range strings.Split(strings.ToLower(text), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "enabled:") {
			return strings.Contains(line, "true")
		}
	}
	return false
}

func newHiddenWevtutilCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("wevtutil.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func isTaskHistoryChannelEnabled() (bool, error) {
	cmd := newHiddenWevtutilCmd("gl", "Microsoft-Windows-TaskScheduler/Operational")
	out, err := runCommandWithTimeout(cmd, metadataCommandTimeout)
	if err != nil {
		return false, err
	}
	return parseChannelEnabledFromText(out), nil
}

func enableTaskHistoryChannel() error {
	cmd := newHiddenWevtutilCmd("sl", "Microsoft-Windows-TaskScheduler/Operational", "/e:true")
	out, err := runCommandWithTimeout(cmd, metadataCommandTimeout)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

func queryTaskExecutionHistory(taskName string, limit int) ([]TaskExecutionRecord, error) {
	query := "*[System[(EventID=100 or EventID=102 or EventID=200 or EventID=201 or EventID=322)]]"
	cleanName := strings.TrimSpace(taskName)
	if cleanName != "" {
		if !strings.HasPrefix(cleanName, `\`) {
			cleanName = `\` + cleanName
		}
		query = fmt.Sprintf("*[EventData[Data[@Name='TaskName']='%s']] and %s", cleanName, query)
	}

	cmdArgs := []string{"qe", "Microsoft-Windows-TaskScheduler/Operational", "/q:" + query, "/f:XML", "/rd:true"}
	timeout := metadataCommandTimeout
	if limit > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("/c:%d", limit*4+8))
		if limit > 1000 {
			timeout = 30 * time.Second
		}
	} else {
		// limit <= 0 表示不限制条数（全量提取）
		timeout = 30 * time.Second
	}

	cmd := newHiddenWevtutilCmd(cmdArgs...)
	out, err := runCommandWithTimeout(cmd, timeout)
	if err != nil {
		return nil, fmt.Errorf("查询事件日志失败: %w", err)
	}
	events, err := parseWevtutilXML(out)
	if err != nil {
		return nil, err
	}
	records := pairExecutionEvents(events, cleanName)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

// exportTaskHistoryToCSV 将任务执行历史记录导出为带 UTF-8 BOM 的标准 CSV 文件。
func exportTaskHistoryToCSV(records []TaskExecutionRecord, outputPath string) error {
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

	header := []string{"序号", "任务名称", "开始时间", "完成时间", "耗时", "退出代码", "运行状态", "执行程序"}
	if err := w.Write(header); err != nil {
		return err
	}

	for i, r := range records {
		row := []string{
			fmt.Sprintf("%d", i+1),
			r.TaskName,
			r.StartTime,
			r.EndTime,
			r.Duration,
			r.ExitCode,
			r.Status,
			r.Action,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}
