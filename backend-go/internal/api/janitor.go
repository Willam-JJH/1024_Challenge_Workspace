package api

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// janitorLoop 定期回收上传目录里没人要的东西。
func (s *Server) janitorLoop(ctx context.Context) {
	interval := time.Duration(s.cfg.JanitorIntervalSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runJanitor(ctx)
		}
	}
}

// runJanitor 回收两样东西：中断上传留下的分片，以及永远没完成的素材记录。
func (s *Server) runJanitor(ctx context.Context) {
	if s.writePool() == nil {
		return
	}
	grace := time.Duration(s.cfg.UploadGraceHours) * time.Hour
	if grace <= 0 {
		grace = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-grace)

	if removed := removeStaleParts(s.uploadRoot(), cutoff); removed > 0 {
		s.logger.Info("清理中断上传的分片", "files", removed)
	}

	// 超过保留时长仍然是 pending 的素材：上传流程早就断了，连同文件一起删掉。
	rows, err := s.writePool().Query(ctx,
		`SELECT id, storage_key FROM media_assets WHERE status = 'pending' AND created_at < $1`,
		cutoff)
	if err != nil {
		s.logger.Error("清理未完成素材：查询失败", "error", err)
		return
	}
	type stale struct {
		id  string
		key string
	}
	var pending []stale
	for rows.Next() {
		var item stale
		if err := rows.Scan(&item.id, &item.key); err != nil {
			rows.Close()
			s.logger.Error("清理未完成素材：读取失败", "error", err)
			return
		}
		pending = append(pending, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.logger.Error("清理未完成素材：遍历失败", "error", err)
		return
	}

	for _, item := range pending {
		// 先删库再删文件：反过来的话，中途失败会留下没有文件的记录，
		// 接口会一直报 404，比留几个孤儿文件更难排查。
		if _, err := s.writePool().Exec(ctx,
			`DELETE FROM media_assets WHERE id = $1 AND status = 'pending'`, item.id); err != nil {
			s.logger.Error("清理未完成素材：删除记录失败", "asset_id", item.id, "error", err)
			continue
		}
		_ = os.RemoveAll(filepath.Dir(filepath.Join(s.uploadRoot(), filepath.FromSlash(item.key))))
	}
	if len(pending) > 0 {
		s.logger.Info("清理未完成素材", "assets", len(pending))
	}
}

// removeStaleParts 删掉超过截止时间的临时分片，返回删除数量。
//
// 只看文件名里带 .part- 的：那是分片上传途中产生的中间文件，
// 上传一旦中断就不会再有人引用它们。
func removeStaleParts(root string, cutoff time.Time) int {
	removed := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if !strings.Contains(entry.Name(), ".part-") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.ModTime().After(cutoff) {
			return nil
		}
		if os.Remove(path) == nil {
			removed++
		}
		return nil
	})
	return removed
}
