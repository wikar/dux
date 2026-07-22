package warehouse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeImportPathRejectsUnsafeSources(t *testing.T) {
	root := t.TempDir()
	outside, err := os.CreateTemp(filepath.Dir(root), "outside-*.parquet")
	if err != nil {
		t.Fatal(err)
	}
	outside.Close()
	defer os.Remove(outside.Name())

	for _, name := range []string{outside.Name(), filepath.Join("..", filepath.Base(outside.Name())), "unfinished.partial"} {
		if _, err := safeImportPath(root, name); err == nil {
			t.Fatalf("unsafe import path %q accepted", name)
		}
	}
}

func TestSafeImportPathRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.parquet")
	if err := os.WriteFile(target, []byte("not important"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.parquet")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := safeImportPath(root, "link.parquet"); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCopyAndHashHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.parquet")
	if err := os.WriteFile(source, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyAndHash(ctx, source, filepath.Join(root, "copy.partial")); err == nil {
		t.Fatal("canceled copy succeeded")
	}
}
