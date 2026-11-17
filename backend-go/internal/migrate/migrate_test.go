package migrate

import (
	"regexp"
	"strings"
	"testing"
)

var versionPrefix = regexp.MustCompile(`^[0-9]{4}_`)

func TestVersionsAreSortedAndNonEmpty(t *testing.T) {
	names, err := Versions()
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("文件名必须按执行顺序严格递增：%s >= %s", names[i-1], names[i])
		}
	}
	for _, name := range names {
		if !versionPrefix.MatchString(name) {
			t.Fatalf("文件名要以零填充序号开头：%s", name)
		}
	}
}

func TestEmbeddedSQLIsRunnable(t *testing.T) {
	names, err := Versions()
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	for _, name := range names {
		body, err := sqlFiles.ReadFile("sql/" + name)
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		sql := string(body)
		if strings.TrimSpace(sql) == "" {
			t.Fatalf("%s 是空文件", name)
		}
		if !strings.Contains(sql, "-- Running upgrade") {
			t.Fatalf("%s 缺少 Alembic 的升级标记，可能是导出脚本出错", name)
		}
		// 事务包装必须被剥掉：Run 会在自己的事务里执行整个文件，
		// 嵌套的 BEGIN/COMMIT 会让「DDL 与记账同生共死」失效。
		for _, line := range strings.Split(sql, "\n") {
			switch strings.ToUpper(strings.TrimSpace(line)) {
			case "BEGIN;", "COMMIT;":
				t.Fatalf("%s 里还有事务包装行 %q", name, strings.TrimSpace(line))
			}
		}
	}
}
