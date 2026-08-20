package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func testManifest() manifest {
	return manifest{
		Schema:       manifestSchema,
		Product:      productName,
		Version:      "0.2.0",
		SourceURL:    "https://github.com/LightHaru/codex-relay/releases/download/v0.3.0/source.zip",
		SourceSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ReleaseURL:   "https://github.com/LightHaru/codex-relay/releases/tag/v0.3.0",
	}
}

func TestValidateManifest(t *testing.T) {
	if err := validateManifest(testManifest()); err != nil {
		t.Fatal(err)
	}
	invalid := testManifest()
	invalid.SourceURL = "https://example.invalid/source.zip"
	if err := validateManifest(invalid); err == nil {
		t.Fatal("expected unapproved source host to fail")
	}
	invalid = testManifest()
	invalid.SourceSHA256 = "not-a-hash"
	if err := validateManifest(invalid); err == nil {
		t.Fatal("expected invalid hash to fail")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		newer       bool
	}{
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.1.0", false},
		{"v2.0.0", "1.9.9", true},
	} {
		got, err := compareVersions(test.left, test.right)
		if err != nil || got != test.newer {
			t.Fatalf("compareVersions(%q, %q) = %v, %v", test.left, test.right, got, err)
		}
	}
	if _, err := compareVersions("1.2", "1.0.0"); err == nil {
		t.Fatal("expected malformed version to fail")
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unsafe.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("no"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractZipSafe(archivePath, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("expected traversal archive to fail")
	}
}

func TestFindSourceRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "release", "scripts")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "install_windows.ps1")
	if err := os.WriteFile(path, []byte("# test"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := findSourceRoot(filepath.Join(root, "release"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "release") {
		t.Fatalf("findSourceRoot = %s", got)
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("router"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("router"))
	got, err := fileSHA256(path)
	if err != nil || got != hex.EncodeToString(digest[:]) {
		t.Fatalf("fileSHA256 = %s, %v", got, err)
	}
}
