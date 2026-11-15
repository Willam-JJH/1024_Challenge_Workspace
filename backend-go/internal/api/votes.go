package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	boardPattern       = "GET /api/v1/votes"
	bigScreenPattern   = "GET /api/v1/votes/big-screen"
	boardStreamPattern = "GET /api/v1/votes/stream"
)

var errNoCurrentEvent = errors.New("current event is not configured")

type boardRow struct {
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Votes     int    `json:"votes"`
	Rank      int    `json:"rank"`
}

type boardOut struct {
	Items      []boardRow `json:"items"`
	TotalVotes int        `json:"total_votes"`
	UpdatedAt  string     `json:"updated_at"`
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	s.writeBoard(w, r, 0)
}

func (s *Server) bigScreen(w http.ResponseWriter, r *http.Request) {
	n := 10
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 50 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		n = parsed
	}
	s.writeBoard(w, r, n)
}

func (s *Server) writeBoard(w http.ResponseWriter, r *http.Request, limit int) {
	board, err := s.buildBoard(r.Context(), true)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if limit > 0 && len(board.Items) > limit {
		board.Items = board.Items[:limit]
	}
	writeJSON(w, http.StatusOK, board)
}

func (s *Server) buildBoard(ctx context.Context, zeroWhenHidden bool) (*boardOut, error) {
	pool := s.readPool()
	if pool == nil {
		return nil, errInternal
	}

	var eventID string
	var votesVisible bool
	err := pool.QueryRow(ctx,
		`SELECT id, votes_visible FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&eventID, &votesVisible)
	if err != nil {
		if isNoRows(err) {
			return nil, errNoCurrentEvent
		}
		return nil, err
	}

	counts := map[string]int{}
	rows, err := pool.Query(ctx, `
		SELECT company_id, count(*)
		FROM votes
		WHERE event_id = $1 AND status = 'live'
		GROUP BY company_id`, eventID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var companyID string
		var count int
		if err := rows.Scan(&companyID, &count); err != nil {
			rows.Close()
			return nil, err
		}
		counts[companyID] = count
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	// 竞赛排名：同分同名次，下一名次跳过。
	type counted struct {
		id    string
		votes int
	}
	ordered := make([]counted, 0, len(counts))
	for id, votes := range counts {
		ordered = append(ordered, counted{id: id, votes: votes})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].votes != ordered[j].votes {
			return ordered[i].votes > ordered[j].votes
		}
		return ordered[i].id < ordered[j].id
	})
	rankOf := map[string]int{}
	previousVotes := -1
	previousRank := 0
	for index, entry := range ordered {
		if entry.votes != previousVotes {
			previousRank = index + 1
			previousVotes = entry.votes
		}
		rankOf[entry.id] = previousRank
	}

	companyRows, err := pool.Query(ctx, `
		SELECT id, name, slug FROM companies
		WHERE event_id = $1 ORDER BY name`, eventID)
	if err != nil {
		return nil, err
	}
	items := make([]boardRow, 0, 32)
	for companyRows.Next() {
		var row boardRow
		if err := companyRows.Scan(&row.CompanyID, &row.Name, &row.Slug); err != nil {
			companyRows.Close()
			return nil, err
		}
		row.Votes = counts[row.CompanyID]
		rank, ok := rankOf[row.CompanyID]
		if !ok {
			rank = len(counts) + 1
		}
		row.Rank = rank
		items = append(items, row)
	}
	companyRows.Close()
	if companyRows.Err() != nil {
		return nil, companyRows.Err()
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Votes != items[j].Votes {
			return items[i].Votes > items[j].Votes
		}
		if items[i].Rank != items[j].Rank {
			return items[i].Rank < items[j].Rank
		}
		return items[i].Name < items[j].Name
	})

	total := 0
	for _, item := range items {
		total += item.Votes
	}
	if !votesVisible && zeroWhenHidden {
		// 与 Python 一致：未公开时榜单可见但没有数字。
		for i := range items {
			items[i].Votes = 0
			items[i].Rank = 0
		}
		total = 0
	}
	return &boardOut{
		Items:      items,
		TotalVotes: total,
		UpdatedAt:  formatTime(time.Now().UTC()),
	}, nil
}

// streamBoard 通过广播器下发榜单：所有连接共用一次查询，内容变化时才推 event，
// 其余时间只发心跳保活。
func (s *Server) streamBoard(w http.ResponseWriter, r *http.Request) {
	// 没有数据库就没有实时数据可推。明确拒绝，而不是挂一条永远收不到内容的连接。
	if s.writePool() == nil || s.boardBroker == nil {
		writeError(w, r, http.StatusServiceUnavailable, "unavailable", "实时数据暂不可用")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	updates, cancel := s.boardBroker.Subscribe()
	defer cancel()

	if frame, ok := s.boardBroker.Snapshot(); ok {
		if !writeSSEFrame(w, controller, "board", frame) {
			return
		}
	}

	ping := time.NewTicker(2 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-updates:
			if !writeSSEFrame(w, controller, "board", frame) {
				return
			}
		case <-ping.C:
			if !writeSSEPing(w, controller) {
				return
			}
		}
	}
}

// writeSSEFrame 写一帧并立即冲刷；返回 false 表示连接已经断了。
func writeSSEFrame(w http.ResponseWriter, controller *http.ResponseController,
	event string, frame []byte) bool {
	if _, err := fmt.Fprintf(w, `event: %s
data: %s

`, event, frame); err != nil {
		return false
	}
	return controller.Flush() == nil
}

// writeSSEPing 发一行注释作为心跳——SSE 里注释不会触发客户端事件。
func writeSSEPing(w http.ResponseWriter, controller *http.ResponseController) bool {
	if _, err := fmt.Fprint(w, `: ping

`); err != nil {
		return false
	}
	return controller.Flush() == nil
}
