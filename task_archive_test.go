package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func taskXMLWithURI(name string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><URI>` + name + `</URI></RegistrationInfo><Actions><Exec><Command>cmd.exe</Command></Exec></Actions></Task>`
}

func TestTaskArchiveRoundTripKeepsTaskNameAndXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.zip")
	items := []TaskArchiveItem{{Name: `\Folder\Daily`, XML: taskXMLWithURI(`\Folder\Daily`)}}
	if err := writeTaskArchive(path, "SERVER-A", items); err != nil {
		t.Fatal(err)
	}
	manifest := readArchiveManifest(t, path)
	if len(manifest.Tasks) != 1 || manifest.Tasks[0].File != "tasks/Folder_Daily.xml" {
		t.Fatalf("manifest filename = %#v, want tasks/Folder_Daily.xml", manifest.Tasks)
	}
	got, err := readTaskArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != items[0].Name || got[0].XML != items[0].XML {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestArchiveTaskXMLFileNameFlattensTaskPath(t *testing.T) {
	if got := archiveTaskXMLFileName(`\DouyinUser\DouyinGuard\LaunchDouyinGuard`); got != "DouyinUser_DouyinGuard_LaunchDouyinGuard.xml" {
		t.Fatalf("archiveTaskXMLFileName() = %q", got)
	}
	if got := archiveTaskXMLFileName(`\A:B*?`); got != "A_B__.xml" {
		t.Fatalf("archiveTaskXMLFileName() sanitization = %q", got)
	}
}

func TestUniqueArchiveTaskXMLFileNameAvoidsFlattenedCollisions(t *testing.T) {
	used := map[string]bool{}
	first := uniqueArchiveTaskXMLFileName(`\A\B`, used)
	second := uniqueArchiveTaskXMLFileName(`\A_B`, used)
	if first != "A_B.xml" || second != "A_B_2.xml" {
		t.Fatalf("got %q and %q", first, second)
	}
}

func TestReadTaskArchiveRejectsTraversalAndBadDigest(t *testing.T) {
	for _, path := range []string{
		makeArchiveWithManifestEntry(t, "../task.xml", taskXMLWithURI(`\Bad`), ""),
		makeArchiveWithManifestEntry(t, "tasks/0001.xml", taskXMLWithURI(`\Bad`), "0000"),
	} {
		if _, err := readTaskArchive(path); err == nil {
			t.Fatalf("expected invalid archive: %s", path)
		}
	}
}

func TestReadTaskArchiveSupportsLegacyNumericEntryName(t *testing.T) {
	xmlText := taskXMLWithURI(`\Legacy`)
	digest := sha256.Sum256([]byte(xmlText))
	path := makeArchiveWithManifestEntry(t, "tasks/0001.xml", xmlText, hex.EncodeToString(digest[:]))
	items, err := readTaskArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != `\Legacy` {
		t.Fatalf("legacy archive items = %#v", items)
	}
}

func TestCollectTaskArchiveItemsContinuesAfterExportFailure(t *testing.T) {
	summary := collectTaskArchiveItems([]Task{{Name: `\Good`}, {Name: `\Bad`}}, func(name string) (string, error) {
		if name == `\Bad` {
			return "", errors.New("denied")
		}
		return taskXMLWithURI(name), nil
	})
	if len(summary.Items) != 1 || summary.Items[0].Name != `\Good` {
		t.Fatal("good task not collected")
	}
	if len(summary.Results) != 2 || summary.Results[1].Err == nil {
		t.Fatal("failure not retained")
	}
}

func TestApplyTaskImportPlanSkipsAndContinuesAfterFailure(t *testing.T) {
	plan := []TaskImportPlanItem{
		{Item: TaskArchiveItem{Name: `\Skip`}, Skip: true},
		{Item: TaskArchiveItem{Name: `\Fail`}, Overwrite: true},
		{Item: TaskArchiveItem{Name: `\Good`}},
	}
	results := applyTaskImportPlan(plan, func(name, _ string) error {
		if name == `\Fail` {
			return errors.New("denied")
		}
		return nil
	})
	if results[0].Err != nil || !results[0].Skipped || results[1].Err == nil || results[2].Err != nil {
		t.Fatal("unexpected import result")
	}
}

func TestBuildTaskImportPlanMarksExistingNames(t *testing.T) {
	items := []TaskArchiveItem{{Name: `\Existing`}, {Name: `\New`}}
	plan := buildTaskImportPlan(items, map[string]bool{`\Existing`: true})
	if !plan[0].Exists || plan[1].Exists {
		t.Fatal("existing task detection mismatch")
	}
}

func TestTasksForExportUsesSelectionOtherwiseFiltered(t *testing.T) {
	all := []Task{{Name: `\A`}, {Name: `\B`}}
	if got := tasksForExport(&all[1], all); len(got) != 1 || got[0].Name != `\B` {
		t.Fatal("selected task scope mismatch")
	}
	if got := tasksForExport(nil, all); len(got) != 2 {
		t.Fatal("filtered task scope mismatch")
	}
}

func TestArchiveCompletionRequestsAsyncRefresh(t *testing.T) {
	if !archiveCompletionRefresh() {
		t.Fatal("archive completion must refresh the task list asynchronously")
	}
}

func readArchiveManifest(t *testing.T, path string) TaskArchiveManifest {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != "manifest.json" {
			continue
		}
		content, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer content.Close()
		var manifest TaskArchiveManifest
		if decodeErr := json.NewDecoder(content).Decode(&manifest); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return manifest
	}
	t.Fatal("manifest.json missing")
	return TaskArchiveManifest{}
}

func makeArchiveWithManifestEntry(t *testing.T, entryName, xmlText, digest string) string {
	t.Helper()
	name, err := taskNameFromXML(xmlText)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "invalid.zip")
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest := TaskArchiveManifest{
		FormatVersion: taskArchiveFormatVersion,
		Tasks:         []TaskArchiveManifestEntry{{Name: name, File: entryName, SHA256: digest}},
	}
	manifestFile, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(manifestFile).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	if entryName != "../task.xml" {
		xmlFile, err := writer.Create(entryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := xmlFile.Write([]byte(xmlText)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
