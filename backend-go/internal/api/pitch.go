package api

import (
	"context"
	"net/http"
	"time"

	"challenge-api/internal/auth"
)

const (
	pitchPattern         = "GET /api/v1/pitch"
	pitchSourcesPattern  = "GET /api/v1/pitch/sources"
	pitchMessagesPattern = "GET /api/v1/pitch/messages"
	pitchMessageCreate   = "POST /api/v1/pitch/messages"
	pitchMessageDelete   = "DELETE /api/v1/pitch/messages/{message_id}"
	pitchStreamPattern   = "GET /api/v1/pitch/stream"
)

type sourceOut struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Region   string `json:"region"`
	Enabled  bool   `json:"enabled"`
	Position int    `json:"position"`
}

type messageOut struct {
	ID         string `json:"id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	CreatedAt  string `json:"created_at"`
}

type pitchStateOut struct {
	OnAir    bool         `json:"on_air"`
	Title    string       `json:"title"`
	Sources  []sourceOut  `json:"sources"`
	Messages []messageOut `json:"messages"`
}

type pitchEvent struct {
	ID    string
	OnAir bool
	Title string
}

func (s *Server) pitchEvent(r *http.Request) (*pitchEvent, error) {
	pool := s.readPool()
	if pool == nil {
		return nil, errInternal
	}
	var event pitchEvent
	err := pool.QueryRow(r.Context(),
		`SELECT id, pitch_on_air, pitch_title FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&event.ID, &event.OnAir, &event.Title)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

func (s *Server) pitchSourcesFor(w http.ResponseWriter, r *http.Request, eventID string) ([]sourceOut, bool) {
	rows, err := s.readPool().Query(r.Context(), `
		SELECT id, kind, name, url, region, enabled, position
		FROM pitch_sources
		WHERE event_id = $1 AND enabled
		ORDER BY position, created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()

	sources := make([]sourceOut, 0, 4)
	for rows.Next() {
		var source sourceOut
		if err := rows.Scan(&source.ID, &source.Kind, &source.Name, &source.URL,
			&source.Region, &source.Enabled, &source.Position); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		sources = append(sources, source)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return sources, true
}

func (s *Server) pitchMessagesFor(w http.ResponseWriter, r *http.Request,
	eventID string, limit int) ([]messageOut, bool) {
	rows, err := s.readPool().Query(r.Context(), `
		SELECT m.id, m.author_id, u.display_name, m.body, m.created_at
		FROM pitch_messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.event_id = $1 AND m.status = 'visible'
		ORDER BY m.created_at DESC
		LIMIT $2`, eventID, limit)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()

	messages := make([]messageOut, 0, limit)
	for rows.Next() {
		var message messageOut
		var createdAt time.Time
		if err := rows.Scan(&message.ID, &message.AuthorID, &message.AuthorName,
			&message.Body, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		message.CreatedAt = formatTime(createdAt)
		messages = append(messages, message)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return messages, true
}

// buildPitchState 组装直播页状态。stream=true 时留言时间用 Python 的
// isoformat（+00:00），否则用 pydantic 的 ...Z 写法。
func (s *Server) buildPitchState(ctx context.Context, stream bool) (*pitchStateOut, error) {
	pool := s.readPool()
	if pool == nil {
		return nil, errInternal
	}
	var event pitchEvent
	err := pool.QueryRow(ctx,
		`SELECT id, pitch_on_air, pitch_title FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&event.ID, &event.OnAir, &event.Title)
	if err != nil {
		if isNoRows(err) {
			return nil, errNoCurrentEvent
		}
		return nil, err
	}

	sourceRows, err := pool.Query(ctx, `
		SELECT id, kind, name, url, region, enabled, position
		FROM pitch_sources
		WHERE event_id = $1 AND enabled
		ORDER BY position, created_at`, event.ID)
	if err != nil {
		return nil, err
	}
	sources := make([]sourceOut, 0, 4)
	for sourceRows.Next() {
		var source sourceOut
		if err := sourceRows.Scan(&source.ID, &source.Kind, &source.Name, &source.URL,
			&source.Region, &source.Enabled, &source.Position); err != nil {
			sourceRows.Close()
			return nil, err
		}
		sources = append(sources, source)
	}
	sourceRows.Close()
	if sourceRows.Err() != nil {
		return nil, sourceRows.Err()
	}

	messageRows, err := pool.Query(ctx, `
		SELECT m.id, m.author_id, u.display_name, m.body, m.created_at
		FROM pitch_messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.event_id = $1 AND m.status = 'visible'
		ORDER BY m.created_at DESC
		LIMIT 20`, event.ID)
	if err != nil {
		return nil, err
	}
	messages := make([]messageOut, 0, 20)
	for messageRows.Next() {
		var message messageOut
		var createdAt time.Time
		if err := messageRows.Scan(&message.ID, &message.AuthorID, &message.AuthorName,
			&message.Body, &createdAt); err != nil {
			messageRows.Close()
			return nil, err
		}
		if stream {
			message.CreatedAt = formatTimeOffset(createdAt)
		} else {
			message.CreatedAt = formatTime(createdAt)
		}
		messages = append(messages, message)
	}
	messageRows.Close()
	if messageRows.Err() != nil {
		return nil, messageRows.Err()
	}
	// Python 取最新 20 条后反转成时间正序。
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return &pitchStateOut{
		OnAir:    event.OnAir,
		Title:    event.Title,
		Sources:  sources,
		Messages: messages,
	}, nil
}

func (s *Server) pitch(w http.ResponseWriter, r *http.Request) {
	state, err := s.buildPitchState(r.Context(), false)
	if err != nil {
		if err == errNoCurrentEvent {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) pitchSources(w http.ResponseWriter, r *http.Request) {
	event, err := s.pitchEvent(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if event == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	sources, ok := s.pitchSourcesFor(w, r, event.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

func (s *Server) pitchMessages(w http.ResponseWriter, r *http.Request) {
	event, err := s.pitchEvent(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if event == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	messages, ok := s.pitchMessagesFor(w, r, event.ID, 50)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

type pitchMessageIn struct {
	Body *string `json:"body"`
}

func (s *Server) createPitchMessage(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in pitchMessageIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Body == nil || len([]rune(*in.Body)) < 1 || len([]rune(*in.Body)) > 280 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO pitch_messages (id, event_id, author_id, body, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'visible', $5, $5)`,
		id, eventID, principal.User.ID, *in.Body, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, messageOut{
		ID:         id,
		AuthorID:   principal.User.ID,
		AuthorName: principal.User.DisplayName,
		Body:       *in.Body,
		CreatedAt:  formatTime(now),
	})
}

func (s *Server) deletePitchMessage(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	messageID := r.PathValue("message_id")
	var authorID string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT author_id FROM pitch_messages WHERE id = $1`, messageID).Scan(&authorID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "留言不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if authorID != principal.User.ID && principal.User.Role != "staff" &&
		principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只能撤回自己的留言")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE pitch_messages SET status = 'hidden', updated_at = $2 WHERE id = $1`,
		messageID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// pitchStream 通过广播器下发直播状态：所有连接共用一次查询。
func (s *Server) pitchStream(w http.ResponseWriter, r *http.Request) {
	// 没有数据库就没有实时数据可推。明确拒绝，而不是挂一条永远收不到内容的连接。
	if s.writePool() == nil || s.pitchBroker == nil {
		writeError(w, r, http.StatusServiceUnavailable, "unavailable", "实时数据暂不可用")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	updates, cancel := s.pitchBroker.Subscribe()
	defer cancel()

	if frame, ok := s.pitchBroker.Snapshot(); ok {
		if !writeSSEFrame(w, controller, "pitch", frame) {
			return
		}
	}

	ping := time.NewTicker(3 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-updates:
			if !writeSSEFrame(w, controller, "pitch", frame) {
				return
			}
		case <-ping.C:
			if !writeSSEPing(w, controller) {
				return
			}
		}
	}
}
