package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	taskArchiveFormatVersion = 1
	maxTaskArchiveXMLBytes   = 10 * 1024 * 1024
)

// TaskArchiveItem 是一个可独立恢复的 Windows 原生任务 XML。
type TaskArchiveItem struct {
	Name string
	XML  string
}

type TaskArchiveManifestEntry struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type TaskArchiveManifest struct {
	FormatVersion int                        `json:"formatVersion"`
	ExportedAt    string                     `json:"exportedAt"`
	Source        string                     `json:"source"`
	Tasks         []TaskArchiveManifestEntry `json:"tasks"`
}

// TaskTransferResult 记录一条任务在导入或导出中的最终状态。
type TaskTransferResult struct {
	Name    string
	Skipped bool
	Err     error
}

type TaskExportSummary struct {
	Items   []TaskArchiveItem
	Results []TaskTransferResult
}

type TaskImportPlanItem struct {
	Item      TaskArchiveItem
	Exists    bool
	Overwrite bool
	Skip      bool
}

type taskArchiveXML struct {
	RegistrationInfo struct {
		URI string `xml:"URI"`
	} `xml:"RegistrationInfo"`
}

func taskNameFromXML(xmlText string) (string, error) {
	var definition taskArchiveXML
	if err := xml.Unmarshal([]byte(normalizeTaskXMLText(xmlText)), &definition); err != nil {
		return "", fmt.Errorf("任务 XML 格式无效：%w", err)
	}
	if strings.TrimSpace(definition.RegistrationInfo.URI) == "" {
		return "", errors.New("任务 XML 缺少 RegistrationInfo.URI，无法确定任务名称。")
	}
	return normalizeTaskName(definition.RegistrationInfo.URI)
}

func writeTaskArchive(outputPath, source string, items []TaskArchiveItem) (err error) {
	if len(items) == 0 {
		return errors.New("没有可导出的任务。")
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if !completed {
			_ = os.Remove(outputPath)
		}
	}()

	writer := zip.NewWriter(file)
	defer func() {
		if closeErr := writer.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if strings.TrimSpace(source) == "" {
		source, _ = os.Hostname()
	}
	manifest := TaskArchiveManifest{
		FormatVersion: taskArchiveFormatVersion,
		ExportedAt:    time.Now().Format(time.RFC3339),
		Source:        source,
		Tasks:         make([]TaskArchiveManifestEntry, 0, len(items)),
	}
	names := make(map[string]bool, len(items))
	for index, item := range items {
		name, normalizeErr := normalizeTaskName(item.Name)
		if normalizeErr != nil {
			return normalizeErr
		}
		if names[name] {
			return fmt.Errorf("备份中存在重复任务名称：%s", name)
		}
		names[name] = true
		xmlText := normalizeTaskXMLText(item.XML)
		xmlName, xmlErr := taskNameFromXML(xmlText)
		if xmlErr != nil {
			return xmlErr
		}
		if xmlName != name {
			return fmt.Errorf("任务 XML 名称与任务名称不一致：%s", name)
		}
		filename := fmt.Sprintf("tasks/%04d.xml", index+1)
		entry, createErr := writer.Create(filename)
		if createErr != nil {
			return createErr
		}
		if _, writeErr := io.WriteString(entry, xmlText); writeErr != nil {
			return writeErr
		}
		digest := sha256.Sum256([]byte(xmlText))
		manifest.Tasks = append(manifest.Tasks, TaskArchiveManifestEntry{
			Name: name, File: filename, SHA256: hex.EncodeToString(digest[:]),
		})
	}
	manifestEntry, err := writer.Create("manifest.json")
	if err != nil {
		return err
	}
	if err = json.NewEncoder(manifestEntry).Encode(manifest); err != nil {
		return err
	}
	completed = true
	return nil
}

func readTaskArchive(inputPath string) ([]TaskArchiveItem, error) {
	reader, err := zip.OpenReader(inputPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	entries := make(map[string]*zip.File, len(reader.File))
	var manifestFile *zip.File
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if !safeArchiveEntryName(entry.Name) && entry.Name != "manifest.json" {
			return nil, fmt.Errorf("压缩包包含不安全路径：%s", entry.Name)
		}
		if _, exists := entries[entry.Name]; exists {
			return nil, fmt.Errorf("压缩包包含重复文件：%s", entry.Name)
		}
		entries[entry.Name] = entry
		if entry.Name == "manifest.json" {
			manifestFile = entry
		}
	}
	if manifestFile == nil {
		return nil, errors.New("备份包缺少 manifest.json。")
	}
	manifestBytes, err := readArchiveFile(manifestFile)
	if err != nil {
		return nil, err
	}
	var manifest TaskArchiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("备份清单格式无效：%w", err)
	}
	if manifest.FormatVersion != taskArchiveFormatVersion {
		return nil, fmt.Errorf("不支持的备份格式版本：%d", manifest.FormatVersion)
	}
	if len(manifest.Tasks) == 0 {
		return nil, errors.New("备份包中没有任务。")
	}
	items := make([]TaskArchiveItem, 0, len(manifest.Tasks))
	names := make(map[string]bool, len(manifest.Tasks))
	files := make(map[string]bool, len(manifest.Tasks))
	for _, record := range manifest.Tasks {
		name, nameErr := normalizeTaskName(record.Name)
		if nameErr != nil {
			return nil, fmt.Errorf("备份任务名称无效：%w", nameErr)
		}
		if names[name] {
			return nil, fmt.Errorf("备份包包含重复任务名称：%s", name)
		}
		names[name] = true
		if !safeArchiveEntryName(record.File) {
			return nil, fmt.Errorf("备份任务文件路径不安全：%s", record.File)
		}
		if files[record.File] {
			return nil, fmt.Errorf("备份包包含重复任务文件：%s", record.File)
		}
		files[record.File] = true
		entry := entries[record.File]
		if entry == nil {
			return nil, fmt.Errorf("备份包缺少任务 XML：%s", record.File)
		}
		xmlBytes, readErr := readArchiveFile(entry)
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(xmlBytes)
		if !strings.EqualFold(record.SHA256, hex.EncodeToString(digest[:])) {
			return nil, fmt.Errorf("任务 XML 校验失败：%s", name)
		}
		xmlText := normalizeTaskXMLText(string(xmlBytes))
		xmlName, xmlErr := taskNameFromXML(xmlText)
		if xmlErr != nil {
			return nil, xmlErr
		}
		if xmlName != name {
			return nil, fmt.Errorf("任务 XML 名称与清单不一致：%s", name)
		}
		items = append(items, TaskArchiveItem{Name: name, XML: xmlText})
	}
	return items, nil
}

func readTaskImportFile(inputPath string) ([]TaskArchiveItem, error) {
	if strings.EqualFold(filepath.Ext(inputPath), ".zip") {
		return readTaskArchive(inputPath)
	}
	if !strings.EqualFold(filepath.Ext(inputPath), ".xml") {
		return nil, errors.New("仅支持 ZIP 备份包或 XML 任务文件。")
	}
	info, err := os.Stat(inputPath)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxTaskArchiveXMLBytes {
		return nil, errors.New("XML 任务文件过大。")
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	xmlText := normalizeTaskXMLText(string(data))
	name, err := taskNameFromXML(xmlText)
	if err != nil {
		return nil, err
	}
	return []TaskArchiveItem{{Name: name, XML: xmlText}}, nil
}

func safeArchiveEntryName(name string) bool {
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || !strings.HasPrefix(name, "tasks/") {
		return false
	}
	return path.Clean(name) == name && !strings.Contains(name, "..")
}

func readArchiveFile(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxTaskArchiveXMLBytes {
		return nil, fmt.Errorf("备份文件过大：%s", file.Name)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxTaskArchiveXMLBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTaskArchiveXMLBytes {
		return nil, fmt.Errorf("备份文件过大：%s", file.Name)
	}
	return data, nil
}

func collectTaskArchiveItems(tasks []Task, exporter func(string) (string, error)) TaskExportSummary {
	summary := TaskExportSummary{
		Items:   make([]TaskArchiveItem, 0, len(tasks)),
		Results: make([]TaskTransferResult, 0, len(tasks)),
	}
	for _, task := range tasks {
		name, nameErr := normalizeTaskName(task.Name)
		if nameErr != nil {
			summary.Results = append(summary.Results, TaskTransferResult{Name: task.Name, Err: nameErr})
			continue
		}
		xmlText, exportErr := exporter(name)
		if exportErr != nil {
			summary.Results = append(summary.Results, TaskTransferResult{Name: name, Err: exportErr})
			continue
		}
		xmlText = normalizeTaskXMLText(xmlText)
		xmlName, xmlErr := taskNameFromXML(xmlText)
		if xmlErr != nil {
			summary.Results = append(summary.Results, TaskTransferResult{Name: name, Err: xmlErr})
			continue
		}
		if xmlName != name {
			summary.Results = append(summary.Results, TaskTransferResult{Name: name, Err: errors.New("导出的 XML 任务名称不一致")})
			continue
		}
		summary.Items = append(summary.Items, TaskArchiveItem{Name: name, XML: xmlText})
		summary.Results = append(summary.Results, TaskTransferResult{Name: name})
	}
	return summary
}

func buildTaskImportPlan(items []TaskArchiveItem, existing map[string]bool) []TaskImportPlanItem {
	plan := make([]TaskImportPlanItem, 0, len(items))
	for _, item := range items {
		name, err := normalizeTaskName(item.Name)
		if err != nil {
			plan = append(plan, TaskImportPlanItem{Item: item, Skip: true})
			continue
		}
		item.Name = name
		plan = append(plan, TaskImportPlanItem{Item: item, Exists: existing[name]})
	}
	return plan
}

func applyTaskImportPlan(plan []TaskImportPlanItem, restorer func(string, string) error) []TaskTransferResult {
	results := make([]TaskTransferResult, 0, len(plan))
	for _, item := range plan {
		result := TaskTransferResult{Name: item.Item.Name, Skipped: item.Skip}
		if !item.Skip {
			result.Err = restorer(item.Item.Name, item.Item.XML)
		}
		results = append(results, result)
	}
	return results
}
