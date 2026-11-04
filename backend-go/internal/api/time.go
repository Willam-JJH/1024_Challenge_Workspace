package api

import "time"

// formatTime 复刻 pydantic v2 的 datetime 序列化：UTC、微秒、Z 结尾，
// 例如 2026-11-03T12:19:05.160024Z；整秒时不带小数部分。
func formatTime(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05Z")
	}
	return t.Format("2006-01-02T15:04:05.000000Z")
}

// formatTimeOffset 复刻 Python datetime.isoformat()：偏移量写法 +00:00，
// CSV 导出等非 pydantic 序列化的位置使用。
func formatTimeOffset(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05+00:00")
	}
	return t.Format("2006-01-02T15:04:05.000000+00:00")
}
