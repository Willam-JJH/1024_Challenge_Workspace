package api

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

// 赛季收口（season close）是「一届比赛正式结束」的唯一时点。
//
// 比赛结束满 SeasonCloseAfterDays 天之后，后台任务一次性做三件事：
//  1. 写入 events.archived_at、把 stage 置为 archived、关掉报名与投票开关；
//  2. 该届作品进入前序作品（归档接口只认 archived_at）；
//  3. 参赛身份回落为普通用户：member / captain → participant，管理员与工作人员不动。
//
// 三件事必须同一时刻完成。分三次跑会出现「报名还开着但用户已经不是参赛者」
// 这种自相矛盾的中间态。
func (s *Server) seasonLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.SeasonPollSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Minute
	}
	s.logger.Info("赛季任务已启动", "interval", interval.String())
	s.runSeasonSweep(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runSeasonSweep(ctx)
		}
	}
}

// seasonCloseTime 是一届比赛正式收口的时刻：结束时间 + 保留天数。
// 保留期留给闭幕、结果公示和后续沟通，期满才把作品挪进前序作品。
func seasonCloseTime(endsAt time.Time, days int) time.Time {
	if days < 0 {
		days = 0
	}
	return endsAt.Add(time.Duration(days) * 24 * time.Hour)
}

// seasonClosed 判断当期赛事是否已经进入空档期。
//
// 两种情形算收口：已经写过 archived_at（系统收口过），或者结束时间加保留期已过
// （本次扫描会紧接着把它归档）。ends_at 为空的赛事一律不算收口——那是还没排期的新赛事，
// 按它回落身份会误伤所有人。
func seasonClosed(now time.Time, endsAt, archivedAt *time.Time, closeAfterDays int) bool {
	if archivedAt != nil {
		return true
	}
	if endsAt == nil {
		return false
	}
	return !now.Before(seasonCloseTime(*endsAt, closeAfterDays))
}

func (s *Server) runSeasonSweep(ctx context.Context) {
	if s.writePool() == nil {
		return
	}
	now := time.Now().UTC()
	// 先归档：下面判断身份回落时直接用 archived_at，不必重复算时间。
	s.archiveDueEvents(ctx, now)
	s.rollbackOffseasonRoles(ctx, now)
}

// archiveDueEvents 把结束满保留期、尚未归档的赛事推进往届。
//
// 单条 UPDATE ... RETURNING 完成认领，多实例同时跑也只会成功一次
// （第二个实例的 WHERE archived_at IS NULL 匹配不到任何行）。
func (s *Server) archiveDueEvents(ctx context.Context, now time.Time) {
	days := s.cfg.SeasonCloseAfterDays
	if days < 0 {
		days = 0
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)

	rows, err := s.writePool().Query(ctx, `
		UPDATE events
		SET stage = 'archived', archived_at = $1,
			registration_open = false, voting_open = false, updated_at = $1
		WHERE archived_at IS NULL AND ends_at IS NOT NULL AND ends_at <= $2
		RETURNING id, slug`, now, cutoff)
	if err != nil {
		s.logger.Error("archive events", "error", err)
		return
	}
	type archived struct {
		id   string
		slug string
	}
	var done []archived
	for rows.Next() {
		var row archived
		if err := rows.Scan(&row.id, &row.slug); err != nil {
			rows.Close()
			s.logger.Error("archive events scan", "error", err)
			return
		}
		done = append(done, row)
	}
	rows.Close()
	if rows.Err() != nil {
		s.logger.Error("archive events rows", "error", rows.Err())
		return
	}

	for _, row := range done {
		s.logger.Info("赛事已收口，作品进入前序作品", "event", row.slug, "event_id", row.id)
		note := "season closed after " + strconvItoa(days) + " days"
		if err := s.recordSystemAudit(ctx, "event.archive", "event", row.id, &note); err != nil {
			s.logger.Error("audit event.archive", "error", err)
		}
	}
}

// rollbackOffseasonRoles 在空档期把参赛身份回落为普通用户。
//
// 只降不升，且永远不碰 staff / admin：管理员和工作人员是平台的常驻角色，
// 不随赛季起落。自然幂等——降过之后下一次扫描匹配不到任何行。
func (s *Server) rollbackOffseasonRoles(ctx context.Context, now time.Time) {
	var eventID string
	var endsAt, archivedAt pgtype.Timestamp
	err := s.writePool().QueryRow(ctx, `
		SELECT id, ends_at, archived_at FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&eventID, &endsAt, &archivedAt)
	if isNoRows(err) {
		return
	}
	if err != nil {
		s.logger.Error("season rollback lookup", "error", err)
		return
	}
	if !seasonClosed(now, timestampPtr(endsAt), timestampPtr(archivedAt),
		s.cfg.SeasonCloseAfterDays) {
		return
	}

	tag, err := s.writePool().Exec(ctx, `
		UPDATE users SET role = 'participant', updated_at = $1
		WHERE role IN ('member', 'captain')`, now)
	if err != nil {
		s.logger.Error("season rollback users", "error", err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}
	s.logger.Info("空档期身份回落", "users", tag.RowsAffected(), "event", s.cfg.CurrentEventSlug)
	note := "role reset to participant: " + strconvItoa(int(tag.RowsAffected()))
	if err := s.recordSystemAudit(ctx, "event.offseason_reset", "event", eventID, &note); err != nil {
		s.logger.Error("audit event.offseason_reset", "error", err)
	}
}

// recordSystemAudit 写一条没有操作者的审计（actor_id 为空）。
// audit_log.actor_id 允许为空，后台审计列表把它序列化成 null。
func (s *Server) recordSystemAudit(ctx context.Context, action, targetType, targetID string,
	note *string) error {
	id, err := auth.NewID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO audit_log (id, actor_id, action, target_type, target_id, note,
			created_at, updated_at)
		VALUES ($1, NULL, $2, $3, $4, $5, $6, $6)`,
		id, action, targetType, nilIfEmpty(targetID), note, now)
	return err
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
