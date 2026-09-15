package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInDir(t *testing.T) {
	dir := t.TempDir()

	ok, err := resolveInDir(dir, "a.txt")
	if err != nil {
		t.Fatalf("合法文件名失败：%v", err)
	}
	if filepath.Base(ok) != "a.txt" {
		t.Fatalf("路径 basename 不正确：%s", ok)
	}

	rejects := []string{"", ".", "..", filepath.Join("..", "x"), filepath.Join("a", "b")}
	for _, name := range rejects {
		if _, err := resolveInDir(dir, name); err == nil {
			t.Fatalf("应拒绝文件名 %q", name)
		}
	}

	// 子目录中的文件不能通过裸文件名之外的方式命中
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInDir(dir, "sub"); err != nil {
		t.Fatalf("目录名作为 basename 可以解析（删除时再拒绝非普通文件）：%v", err)
	}
	if _, err := resolveInDir(dir, filepath.Join("sub", "a.txt")); err == nil {
		t.Fatal("应拒绝子路径")
	}
}
