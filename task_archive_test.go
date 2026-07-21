package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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
	got, err := readTaskArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != items[0].Name || got[0].XML != items[0].XML {
		t.Fatalf("round trip mismatch: %#v", got)
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

func makeArchiveWithManifestEntry(t *testing.T, entryName, xmlText, digest string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invalid.zip")
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest := TaskArchiveManifest{
		FormatVersion: taskArchiveFormatVersion,
		Tasks:         []TaskArchiveManifestEntry{{Name: `\Bad`, File: entryName, SHA256: digest}},
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
