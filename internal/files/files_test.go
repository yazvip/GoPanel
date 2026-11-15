package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	return New([]Root{{Token: "root", Label: "Root", Path: dir}}, 1<<20), dir
}

func writeZipArchive(t *testing.T, dir, rel string, entries map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip %q: %v", rel, err)
	}
}

func TestUploadCreatesRelativeDirectories(t *testing.T) {
	svc, dir := newTestService(t)

	entry, err := svc.Upload("root", "", "assets/css/app.css", strings.NewReader("body{}"), false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if entry.Path != "assets/css/app.css" || entry.Name != "app.css" || entry.IsDir {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	data, err := os.ReadFile(filepath.Join(dir, "assets", "css", "app.css"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "body{}" {
		t.Fatalf("content = %q, want %q", data, "body{}")
	}
}

func TestUploadRelativeDirectoriesInsideFolder(t *testing.T) {
	svc, dir := newTestService(t)
	if _, err := svc.Mkdir("root", "site"); err != nil {
		t.Fatalf("mkdir site: %v", err)
	}

	entry, err := svc.Upload("root", "site", "assets/style.css", strings.NewReader("a{}"), false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if entry.Path != "site/assets/style.css" {
		t.Fatalf("path = %q, want %q", entry.Path, "site/assets/style.css")
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "assets", "style.css")); err != nil {
		t.Fatalf("stat uploaded file: %v", err)
	}
}

func TestUploadRejectsEscapingPath(t *testing.T) {
	svc, dir := newTestService(t)
	names := []string{"", ".", "..", "../evil.txt", "css/../../evil.txt", "/etc/passwd", "css//app.css", "..\\evil.txt"}
	for _, name := range names {
		if _, err := svc.Upload("root", "", name, strings.NewReader("x"), false); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("name %q: err = %v, want ErrInvalidName", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaping upload created a file: %v", err)
	}
}

func TestUploadOverwriteRelativeFile(t *testing.T) {
	svc, dir := newTestService(t)
	if _, err := svc.Upload("root", "", "css/app.css", strings.NewReader("v1"), false); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if _, err := svc.Upload("root", "", "css/app.css", strings.NewReader("v2"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("second upload err = %v, want ErrExists", err)
	}
	if _, err := svc.Upload("root", "", "css/app.css", strings.NewReader("v2"), true); err != nil {
		t.Fatalf("overwrite upload: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "css", "app.css"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("content = %q, want %q", data, "v2")
	}
}

func TestUploadRejectsFileAsParentDirectory(t *testing.T) {
	svc, dir := newTestService(t)
	if _, err := svc.Upload("root", "", "app.css", strings.NewReader("body{}"), false); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := svc.Upload("root", "", "app.css/style.css", strings.NewReader("body{}"), false); !errors.Is(err, ErrNotDir) {
		t.Fatalf("err = %v, want ErrNotDir", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "app.css"))
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	if string(data) != "body{}" {
		t.Fatalf("app.css content = %q, want %q", data, "body{}")
	}
}

func TestOpenZipHoldsSelectedItems(t *testing.T) {
	svc, dir := newTestService(t)
	if _, err := svc.Upload("root", "", "notes.txt", strings.NewReader("hello"), false); err != nil {
		t.Fatalf("upload notes: %v", err)
	}
	if _, err := svc.Upload("root", "", "site/index.html", strings.NewReader("<h1>hi</h1>"), false); err != nil {
		t.Fatalf("upload site: %v", err)
	}

	reader, err := svc.OpenZip("root", []string{"notes.txt", "site"})
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := map[string]bool{"notes.txt": true, "site/": true, "site/index.html": true}
	if len(names) != len(want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("unexpected archive entry %q in %v", name, names)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("source file must stay in place: %v", err)
	}
}

func TestOpenZipRejectsMissingPath(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.OpenZip("root", []string{"missing.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := svc.OpenZip("root", nil); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

func TestExtractZipIntoNestedDestination(t *testing.T) {
	svc, dir := newTestService(t)
	writeZipArchive(t, dir, "site/bundle.zip", map[string]string{
		"index.html":  "<h1>hi</h1>",
		"css/app.css": "body{}",
	})

	entry, err := svc.Extract("root", "site/bundle.zip", "backups/2026/bundle")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if entry.Path != "backups/2026/bundle" || !entry.IsDir {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	for name, want := range map[string]string{"index.html": "<h1>hi</h1>", "css/app.css": "body{}"} {
		got, err := os.ReadFile(filepath.Join(dir, "backups", "2026", "bundle", filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read extracted %q: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%q content = %q, want %q", name, got, want)
		}
	}
}

func TestExtractZipDefaultDestination(t *testing.T) {
	svc, dir := newTestService(t)
	writeZipArchive(t, dir, "site/bundle.zip", map[string]string{"readme.txt": "hello"})

	entry, err := svc.Extract("root", "site/bundle.zip", "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if entry.Path != "site/bundle" {
		t.Fatalf("path = %q, want %q", entry.Path, "site/bundle")
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "bundle", "readme.txt")); err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
}

func TestExtractRejectsExistingDestination(t *testing.T) {
	svc, dir := newTestService(t)
	if _, err := svc.Mkdir("root", "site"); err != nil {
		t.Fatalf("mkdir site: %v", err)
	}
	writeZipArchive(t, dir, "site/bundle.zip", map[string]string{"readme.txt": "hello"})

	if _, err := svc.Extract("root", "site/bundle.zip", "site"); !errors.Is(err, ErrExists) {
		t.Fatalf("extract into existing dir err = %v, want ErrExists", err)
	}
}
