package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeFile 在 dir 下建一个文件，并把修改时间设成 modified。
func makeFile(t *testing.T, dir, name string, modified time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}
	return path
}

func TestRemoveStalePartsOnlyRemovesOldPartFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)

	stalePart := makeFile(t, filepath.Join(root, "abc"), "photo.jpg.part-1", old)
	freshPart := makeFile(t, filepath.Join(root, "abc"), "photo.jpg.part-2", now)
	oldOriginal := makeFile(t, filepath.Join(root, "abc"), "photo.jpg", old)
	oldVariant := makeFile(t, filepath.Join(root, "abc"), "w480.webp", old)

	removed := removeStaleParts(root, now.Add(-24*time.Hour))
	if removed != 1 {
		t.Fatalf("应只删除 1 个过期分片，实际 %d", removed)
	}

	if _, err := os.Stat(stalePart); !os.IsNotExist(err) {
		t.Fatal("过期的分片应被删除")
	}
	for _, keep := range []string{freshPart, oldOriginal, oldVariant} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("不该删的文件被删了：%s", keep)
		}
	}
}

func TestRemoveStalePartsOnMissingRootIsNoop(t *testing.T) {
	if removed := removeStaleParts(filepath.Join(t.TempDir(), "不存在"), time.Now()); removed != 0 {
		t.Fatalf("目录不存在时应安静返回，实际删除 %d 个", removed)
	}
}
