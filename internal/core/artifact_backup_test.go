package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBackupFiles(t *testing.T) {
	directory := t.TempDir()
	root, e := os.OpenRoot(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = root.Close() }()
	if e = writeBackupFile(root, "fixture.age", []byte("ciphertext")); e != nil {
		t.Fatal(e)
	}
	if e = writeBackupFile(root, "fixture.age", []byte("replacement")); e == nil {
		t.Fatal("backup overwritten")
	}
	got, e := readBackupFile(root, "fixture.age", 10)
	if e != nil || string(got) != "ciphertext" {
		t.Fatal("file roundtrip failed", e)
	}
	if _, e = readBackupFile(root, "fixture.age", 1); e == nil {
		t.Fatal("oversize backup accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if e = os.WriteFile(outside, []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(directory, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e = readBackupFile(root, "escape", 100); e == nil {
		t.Fatal("backup escaped its root")
	}
	if e = syscall.Mkfifo(filepath.Join(directory, "pipe"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = readBackupFile(root, "pipe", 100); e == nil {
		t.Fatal("nonregular backup accepted")
	}
	info, e := os.Stat(filepath.Join(directory, "fixture.age"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup file permissions")
	}
}
