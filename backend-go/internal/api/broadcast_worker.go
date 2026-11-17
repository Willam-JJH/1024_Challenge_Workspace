package api

import (
	"context"
	"time"
)

// 定时广播 worker：与 Python 的 app/services/broadcast_worker.py 语义一致。
// 先认领（scheduled → sending）再发送，两个实现同时跑也不会重复发。

const (
	broadcastBatch      = 5
	broadcastStuckAfter = time.Hour
)

type dueBroadcast struct {
	ID       string
	Title    string
	Body     string
	Audience string
	Locale   string
}

// StartWorkers 启动后台任务：实时数据的轮询器，以及定时广播。
func (s *Server) StartWorkers(ctx context.Context) {
	// 轮询器先起：没有数据库时它们自己会跳过取数，不影响启动。
	if s.boardBroker != nil {
		go s.boardBroker.Run(ctx)
	}
	if s.pitchBroker != nil {
		go s.pitchBroker.Run(ctx)
	}
	if s.cfg.JanitorIntervalSeconds > 0 {
		go s.janitorLoop(ctx)
	}
	if s.cfg.SeasonPollSeconds > 0 {
		go s.seasonLoop(ctx)
	}
	if s.writePool() == nil || s.cfg.BroadcastPollSeconds <= 0 {
		return
	}
	go s.broadcastLoop(ctx)
}

func (s *Server) broadcastLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.BroadcastPollSeconds) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	s.logger.Info("定时广播 worker 已启动", "interval", interval.String())
	s.runDueBroadcasts(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueBroadcasts(ctx)
		}
	}
}

func (s *Server) runDueBroadcasts(ctx context.Context) {
	pool := s.writePool()
	if pool == nil {
		return
	}
	now := time.Now().UTC()

	// 卡在 sending 超过 1 小时的收尾，避免状态永远卡住（部分发送看邮件日志）
	if _, err := pool.Exec(ctx, `
		UPDATE broadcasts SET status = 'sent', sent_at = $1, updated_at = $1
		WHERE status = 'sending' AND updated_at < $2`, now, now.Add(-broadcastStuckAfter)); err != nil {
		s.logger.Error("broadcast recover", "error", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT id, title, body, audience, locale FROM broadcasts
		WHERE status = 'scheduled' AND scheduled_at IS NOT NULL AND scheduled_at <= $1
		ORDER BY scheduled_at
		LIMIT $2`, now, broadcastBatch)
	if err != nil {
		s.logger.Error("broadcast query", "error", err)
		return
	}
	due := make([]dueBroadcast, 0, broadcastBatch)
	for rows.Next() {
		var item dueBroadcast
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Audience,
			&item.Locale); err != nil {
			rows.Close()
			s.logger.Error("broadcast scan", "error", err)
			return
		}
		due = append(due, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.logger.Error("broadcast rows", "error", err)
		return
	}

	for _, item := range due {
		claimed, err := s.claimBroadcast(ctx, item.ID, now)
		if err != nil {
			s.logger.Error("broadcast claim", "broadcast_id", item.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.deliverBroadcast(ctx, item, now); err != nil {
			// 发送失败就把认领退回去，下一轮重试（与 Python 回滚事务等价）
			s.logger.Error("broadcast deliver", "broadcast_id", item.ID, "error", err)
			if _, revertErr := pool.Exec(ctx, `
				UPDATE broadcasts SET status = 'scheduled', updated_at = $2
				WHERE id = $1 AND status = 'sending'`, item.ID, time.Now().UTC()); revertErr != nil {
				s.logger.Error("broadcast revert", "broadcast_id", item.ID, "error", revertErr)
			}
		}
	}
}

func (s *Server) claimBroadcast(ctx context.Context, id string, now time.Time) (bool, error) {
	tag, err := s.writePool().Exec(ctx, `
		UPDATE broadcasts SET status = 'sending', updated_at = $2
		WHERE id = $1 AND status = 'scheduled'`, id, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Server) deliverBroadcast(ctx context.Context, item dueBroadcast, now time.Time) error {
	users, err := s.emailRecipients(ctx, item.Audience, item.Locale)
	if err != nil {
		return err
	}
	sent, failed := 0, 0
	if len(users) == 0 {
		s.logger.Warn("broadcast 到点发送时没有收件人", "broadcast_id", item.ID)
	} else {
		sent, failed, err = s.sendEmail(ctx, users, item.Title, item.Body, &item.ID)
		if err != nil {
			return err
		}
	}
	if _, err := s.writePool().Exec(ctx, `
		UPDATE broadcasts SET status = 'sent', sent_at = $2, updated_at = $2
		WHERE id = $1`, item.ID, now); err != nil {
		return err
	}
	s.logger.Info("broadcast 已发送", "broadcast_id", item.ID, "sent", sent, "failed", failed)
	return nil
}
