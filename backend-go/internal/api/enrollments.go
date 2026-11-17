package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

// errEnrollmentInCompany 表示用户还是这一届某家公司的成员，要先退出才能改成观众。
var errEnrollmentInCompany = errors.New("仍在公司中")

const (
	enrollPattern           = "POST /api/v1/events/{event_id}/enrollments"
	enrollCancelPattern     = "DELETE /api/v1/events/{event_id}/enrollments/me"
	myEnrollmentsPattern    = "GET /api/v1/me/enrollments"
	adminEnrollmentsPattern = "GET /api/v1/admin/event/enrollments"
)

// 报名身份：参赛者、观众。比赛开始前可以改，取消报名用 DELETE。
var enrollmentKinds = map[string]bool{"participant": true, "audience": true}

type enrollmentEvent struct {
	ID               string
	Slug             string
	Name             string
	RegistrationOpen bool
	StartsAt         *time.Time
	EndsAt           *time.Time
	ArchivedAt       *time.Time
}

func (s *Server) loadEnrollmentEvent(ctx context.Context, q queryer, eventID string) (
	*enrollmentEvent, error) {

	var event enrollmentEvent
	var startsAt, endsAt, archivedAt pgtype.Timestamp
	err := q.QueryRow(ctx, `
		SELECT id, slug, name, registration_open, starts_at, ends_at, archived_at
		FROM events WHERE id = $1`, eventID).
		Scan(&event.ID, &event.Slug, &event.Name, &event.RegistrationOpen,
			&startsAt, &endsAt, &archivedAt)
	if err != nil {
		return nil, err
	}
	event.StartsAt = timestampPtr(startsAt)
	event.EndsAt = timestampPtr(endsAt)
	event.ArchivedAt = timestampPtr(archivedAt)
	return &event, nil
}

// currentEnrollmentEvent 取当期赛事（APP_CURRENT_EVENT_SLUG）。
func (s *Server) currentEnrollmentEvent(ctx context.Context) (*enrollmentEvent, error) {
	var event enrollmentEvent
	var startsAt, endsAt, archivedAt pgtype.Timestamp
	err := s.writePool().QueryRow(ctx, `
		SELECT id, slug, name, registration_open, starts_at, ends_at, archived_at
		FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&event.ID, &event.Slug, &event.Name, &event.RegistrationOpen,
			&startsAt, &endsAt, &archivedAt)
	if err != nil {
		return nil, err
	}
	event.StartsAt = timestampPtr(startsAt)
	event.EndsAt = timestampPtr(endsAt)
	event.ArchivedAt = timestampPtr(archivedAt)
	return &event, nil
}

// inCompanyOfEvent 报告用户是不是这一届某家公司的成员。
func (s *Server) inCompanyOfEvent(ctx context.Context, q queryer, eventID, userID string) (
	bool, error) {

	var one int
	err := q.QueryRow(ctx, `
		SELECT 1 FROM memberships m JOIN companies c ON c.id = m.company_id
		WHERE m.user_id = $1 AND c.event_id = $2 LIMIT 1`, userID, eventID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// enrollmentClosedReason 返回「这一届已经不能再报名」的原因，空串表示还能报。
//
// 三个条件各自独立，缺一个都会出现「比赛结束了还能改身份」这类漏洞。
func enrollmentClosedReason(event *enrollmentEvent, now time.Time) string {
	if event.ArchivedAt != nil {
		return "这一届已经收口，不再接受报名"
	}
	if event.EndsAt != nil && !now.Before(*event.EndsAt) {
		return "这一届已经结束，不再接受报名"
	}
	if !event.RegistrationOpen {
		return "报名尚未开放"
	}
	return ""
}

type enrollmentIn struct {
	Kind string `json:"kind"`
}

type enrollmentOut struct {
	EventID   string `json:"event_id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// createEnrollment 报名参加某一届：参赛者或观众，比赛开始前可以改选。
func (s *Server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in enrollmentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !enrollmentKinds[in.Kind] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	event, err := s.loadEnrollmentEvent(ctx, s.writePool(), r.PathValue("event_id"))
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "赛事不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if reason := enrollmentClosedReason(event, time.Now().UTC()); reason != "" {
		writeError(w, r, http.StatusConflict, "conflict", reason)
		return
	}
	out, err := s.applyEnrollment(ctx, event, principal.User.ID, in.Kind)
	if err != nil {
		if errors.Is(err, errEnrollmentInCompany) {
			writeError(w, r, http.StatusConflict, "conflict", "还在公司里，先退出再切换")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// applyEnrollment 写报名记录并同步用户角色，调用方负责校验赛季是否还能报名。
//
// 写库与角色同步在同一个事务里。角色只做「该升的升、该降的降」这一档，
// 不碰 staff / admin —— 那是平台常驻角色，不随赛季变化。
func (s *Server) applyEnrollment(ctx context.Context, event *enrollmentEvent, userID,
	kind string) (*enrollmentOut, error) {

	// 还在公司里就说明是参赛队成员，不能直接改叫观众，得先退出公司。
	if kind == "audience" {
		member, err := s.inCompanyOfEvent(ctx, s.writePool(), event.ID, userID)
		if err != nil {
			return nil, err
		}
		if member {
			return nil, errEnrollmentInCompany
		}
	}

	id, err := auth.NewID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var out enrollmentOut
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO event_enrollments (id, event_id, user_id, kind, status,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', $5, $5)
		ON CONFLICT (event_id, user_id) DO UPDATE
		SET kind = EXCLUDED.kind, status = 'active', updated_at = EXCLUDED.updated_at
		RETURNING event_id, kind, status, created_at`,
		id, event.ID, userID, kind, now).
		Scan(&out.EventID, &out.Kind, &out.Status, &createdAt)
	if err != nil {
		return nil, err
	}

	if kind == "participant" {
		// 只有最初的普通用户会被提升；管理员、工作人员、队长都不动。
		if _, err := tx.Exec(ctx, `
			UPDATE users SET role = 'member', updated_at = $2
			WHERE id = $1 AND role = 'participant'`, userID, now); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET role = 'participant', updated_at = $2
			WHERE id = $1 AND role = 'member'`, userID, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.CreatedAt = formatTime(createdAt)
	return &out, nil
}

// withdrawEnrollment 取消报名。比赛开始之后不再接受，报名记录保留为 withdrawn。
func (s *Server) withdrawEnrollment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	event, err := s.loadEnrollmentEvent(ctx, s.writePool(), r.PathValue("event_id"))
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "赛事不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	now := time.Now().UTC()

	var enrollmentID, kind string
	err = s.writePool().QueryRow(ctx, `
		SELECT id, kind FROM event_enrollments
		WHERE event_id = $1 AND user_id = $2 AND status = 'active'`,
		event.ID, principal.User.ID).Scan(&enrollmentID, &kind)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "尚未报名")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if event.StartsAt != nil && !now.Before(*event.StartsAt) {
		writeError(w, r, http.StatusConflict, "conflict", "赛事已开始，无法取消报名")
		return
	}
	member, err := s.inCompanyOfEvent(ctx, s.writePool(), event.ID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if member {
		writeError(w, r, http.StatusConflict, "conflict", "还在公司里，先退出再取消报名")
		return
	}

	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE event_enrollments SET status = 'withdrawn', updated_at = $2
		WHERE id = $1`, enrollmentID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if kind == "participant" {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET role = 'participant', updated_at = $2
			WHERE id = $1 AND role = 'member'`, principal.User.ID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type myEnrollmentOut struct {
	EventID   string  `json:"event_id"`
	EventSlug string  `json:"event_slug"`
	EventName string  `json:"event_name"`
	StartsAt  *string `json:"starts_at"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

// myEnrollments 返回自己的全部报名记录，从新到旧。
func (s *Server) myEnrollments(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT en.event_id, e.slug, e.name, e.starts_at, en.kind, en.status, en.created_at
		FROM event_enrollments en JOIN events e ON e.id = en.event_id
		WHERE en.user_id = $1
		ORDER BY e.starts_at DESC NULLS LAST, en.created_at DESC`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	enrollments := make([]myEnrollmentOut, 0, 8)
	for rows.Next() {
		var item myEnrollmentOut
		var startsAt pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.EventID, &item.EventSlug, &item.EventName, &startsAt,
			&item.Kind, &item.Status, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.StartsAt = formatTimePtr(timestampPtr(startsAt))
		item.CreatedAt = formatTime(createdAt)
		enrollments = append(enrollments, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": enrollments})
}

type adminEnrollmentOut struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// adminEnrollments 列出当期赛事的报名名单，可按身份与状态过滤。
func (s *Server) adminEnrollments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	kind := query.Get("kind")
	status := query.Get("status")
	if kind != "" && !enrollmentKinds[kind] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if status != "" && status != "active" && status != "withdrawn" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		limit = parsed
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		offset = parsed
	}

	rows, err := pool.Query(r.Context(), `
		SELECT en.user_id, u.display_name, u.email, en.kind, en.status, en.created_at
		FROM event_enrollments en JOIN users u ON u.id = en.user_id
		WHERE en.event_id = $1
			AND ($2 = '' OR en.kind = $2)
			AND ($3 = '' OR en.status = $3)
		ORDER BY en.created_at DESC
		LIMIT $4 OFFSET $5`, eventID, kind, status, limit, offset)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	items := make([]adminEnrollmentOut, 0, limit)
	for rows.Next() {
		var item adminEnrollmentOut
		var createdAt time.Time
		if err := rows.Scan(&item.UserID, &item.DisplayName, &item.Email, &item.Kind,
			&item.Status, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CreatedAt = formatTime(createdAt)
		items = append(items, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, items)
}
