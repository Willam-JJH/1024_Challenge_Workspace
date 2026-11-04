package api

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// timestampPtr 把可空的时间列转成 *time.Time。
func timestampPtr(value pgtype.Timestamp) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

// formatTimePtr 输出与 pydantic 一致的 null / RFC3339 形式。
func formatTimePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatTime(*value)
	return &formatted
}
