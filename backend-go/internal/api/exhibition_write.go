package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	createCommentPattern = "POST /api/v1/exhibition/projects/{project_id}/comments"
	editCommentPattern   = "PATCH /api/v1/exhibition/comments/{comment_id}"
	deleteCommentPattern = "DELETE /api/v1/exhibition/comments/{comment_id}"
	reportCommentPattern = "POST /api/v1/exhibition/comments/{comment_id}/reports"
	createPromoPattern   = "POST /api/v1/exhibition/promotions"
	editPromoPattern     = "PATCH /api/v1/exhibition/promotions/{promotion_id}"
	deletePromoPattern   = "DELETE /api/v1/exhibition/promotions/{promotion_id}"
	passportPattern      = "GET /api/v1/exhibition/visits"
	stampPattern         = "PUT /api/v1/exhibition/visits/{project_id}"
	removeStampPattern   = "DELETE /api/v1/exhibition/visits/{project_id}"
	companyStampsPattern = "GET /api/v1/exhibition/companies/{company_id}/visits"

	passportNeeded   = 3
	maxLivePromotion = 3
)

type commentIn struct {
	Body     string  `json:"body"`
	ParentID *string `json:"parent_id"`
}

func validCommentBody(body string) bool {
	size := len([]rune(body))
	return size >= 1 && size <= 2000
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in commentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validCommentBody(in.Body) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	projectID := r.PathValue("project_id")
	var exists bool
	if err := s.writePool().QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)`, projectID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在")
		return
	}
	if in.ParentID != nil {
		var parentProject string
		var parentStatus string
		var parentParent pgtype.Text
		err := s.writePool().QueryRow(ctx,
			`SELECT project_id, status, parent_id FROM comments WHERE id = $1`, *in.ParentID).
			Scan(&parentProject, &parentStatus, &parentParent)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil &&
			(parentProject != projectID || parentStatus != "visible" || parentParent.Valid)) {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "回复的评论不存在")
			return
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			s.writeQueryError(w, r, err)
			return
		}
	}

	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO comments (id, project_id, author_id, parent_id, body, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'visible', $6, $6)`,
		id, projectID, principal.User.ID, in.ParentID, in.Body, now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentOut{
		ID:         id,
		ProjectID:  projectID,
		AuthorID:   principal.User.ID,
		AuthorName: principal.User.DisplayName,
		ParentID:   in.ParentID,
		Body:       in.Body,
		CreatedAt:  formatTime(now),
	})
}

// ownComment 复刻 Python 的 _own_comment。
func (s *Server) ownComment(w http.ResponseWriter, r *http.Request,
	principal *auth.Principal) (*commentRow, bool) {
	var row commentRow
	var parent pgtype.Text
	var createdAt time.Time
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, project_id, author_id, parent_id, body, status, created_at
		FROM comments WHERE id = $1`, r.PathValue("comment_id")).
		Scan(&row.ID, &row.ProjectID, &row.AuthorID, &parent, &row.Body, &row.Status, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Status == "deleted") {
		writeError(w, r, http.StatusNotFound, "not_found", "评论不存在")
		return nil, false
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.ParentID = textPtr(parent)
	row.CreatedAt = createdAt
	if row.AuthorID != principal.User.ID && principal.User.Role != "staff" &&
		principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只能操作自己的评论")
		return nil, false
	}
	return &row, true
}

type commentRow struct {
	ID        string
	ProjectID string
	AuthorID  string
	ParentID  *string
	Body      string
	Status    string
	CreatedAt time.Time
}

func (s *Server) editComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.ownComment(w, r, principal)
	if !ok {
		return
	}
	var in commentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validCommentBody(in.Body) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE comments SET body = $2, updated_at = $3 WHERE id = $1`,
		row.ID, in.Body, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	authorName := principal.User.DisplayName
	if row.AuthorID != principal.User.ID {
		var name string
		if err := s.writePool().QueryRow(r.Context(),
			`SELECT display_name FROM users WHERE id = $1`, row.AuthorID).Scan(&name); err == nil {
			authorName = name
		} else {
			authorName = "已注销"
		}
	}
	writeJSON(w, http.StatusOK, commentOut{
		ID:         row.ID,
		ProjectID:  row.ProjectID,
		AuthorID:   row.AuthorID,
		AuthorName: authorName,
		ParentID:   row.ParentID,
		Body:       in.Body,
		CreatedAt:  formatTime(row.CreatedAt),
	})
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.ownComment(w, r, principal)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE comments SET status = 'deleted', updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) reportComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	commentID := r.PathValue("comment_id")
	var authorID, status string
	err := s.writePool().QueryRow(ctx,
		`SELECT author_id, status FROM comments WHERE id = $1`, commentID).
		Scan(&authorID, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "visible") {
		writeError(w, r, http.StatusNotFound, "not_found", "评论不存在")
		return
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if authorID == principal.User.ID {
		writeError(w, r, http.StatusConflict, "conflict", "不能举报自己的评论")
		return
	}
	var reported bool
	if err := s.writePool().QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM comment_reports WHERE comment_id = $1 AND reporter_id = $2)`,
		commentID, principal.User.ID).Scan(&reported); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if reported {
		writeError(w, r, http.StatusConflict, "conflict", "已经举报过这条评论")
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO comment_reports (id, comment_id, reporter_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)`, id, commentID, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type promotionIn struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

func validPromotion(in promotionIn) bool {
	size := len([]rune(in.Body))
	return (in.Kind == "wall" || in.Kind == "ticker") && size >= 1 && size <= 280
}

func (s *Server) myCompanyID(ctx context.Context, userID string) (string, error) {
	var companyID string
	err := s.writePool().QueryRow(ctx,
		`SELECT company_id FROM memberships WHERE user_id = $1 LIMIT 1`, userID).Scan(&companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return companyID, err
}

func (s *Server) createPromotion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in promotionIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validPromotion(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	companyID, err := s.myCompanyID(ctx, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if companyID == "" {
		writeError(w, r, http.StatusForbidden, "forbidden", "需要先加入一家公司")
		return
	}
	var live int
	if err := s.writePool().QueryRow(ctx, `
		SELECT count(*) FROM promotions WHERE company_id = $1 AND status = 'live'`,
		companyID).Scan(&live); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if live >= maxLivePromotion {
		writeError(w, r, http.StatusConflict, "conflict",
			"每家公司最多 3 条在线广告")
		return
	}
	var companyName string
	if err := s.writePool().QueryRow(ctx,
		`SELECT name FROM companies WHERE id = $1`, companyID).Scan(&companyName); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO promotions (id, company_id, kind, body, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'live', $5, $5)`,
		id, companyID, in.Kind, in.Body, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, promotionOut{
		ID:          id,
		CompanyID:   companyID,
		CompanyName: companyName,
		Kind:        in.Kind,
		Body:        in.Body,
		CreatedAt:   formatTime(now),
	})
}

func (s *Server) ownPromotion(w http.ResponseWriter, r *http.Request,
	principal *auth.Principal) (*promotionOut, bool) {
	var row promotionOut
	var createdAt time.Time
	err := s.writePool().QueryRow(r.Context(), `
		SELECT p.id, p.company_id, c.name, p.kind, p.body, p.created_at
		FROM promotions p JOIN companies c ON c.id = p.company_id
		WHERE p.id = $1`, r.PathValue("promotion_id")).
		Scan(&row.ID, &row.CompanyID, &row.CompanyName, &row.Kind, &row.Body, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "not_found", "广告不存在")
		return nil, false
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.CreatedAt = formatTime(createdAt)
	if principal.User.Role != "staff" && principal.User.Role != "admin" {
		var member bool
		if err := s.writePool().QueryRow(r.Context(), `
			SELECT EXISTS(SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2)`,
			row.CompanyID, principal.User.ID).Scan(&member); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		if !member {
			writeError(w, r, http.StatusForbidden, "forbidden", "只能操作本公司的广告")
			return nil, false
		}
	}
	return &row, true
}

func (s *Server) editPromotion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.ownPromotion(w, r, principal)
	if !ok {
		return
	}
	var in promotionIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validPromotion(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE promotions SET kind = $2, body = $3, updated_at = $4 WHERE id = $1`,
		row.ID, in.Kind, in.Body, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) deletePromotion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.ownPromotion(w, r, principal)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE promotions SET status = 'hidden', updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type visitOut struct {
	ProjectID    string  `json:"project_id"`
	CompanyName  string  `json:"company_name"`
	ProjectTitle string  `json:"project_title"`
	Note         *string `json:"note"`
	CreatedAt    string  `json:"created_at"`
}

type passportOut struct {
	Items    []visitOut `json:"items"`
	Needed   int        `json:"needed"`
	Complete bool       `json:"complete"`
}

func (s *Server) myPassport(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		if err == errNoCurrentEvent {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(ctx, `
		SELECT p.id, c.name, p.title, v.note, v.created_at
		FROM visits v
		JOIN projects p ON p.id = v.project_id
		JOIN companies c ON c.id = p.company_id
		WHERE v.visitor_id = $1 AND v.event_id = $2
		ORDER BY v.created_at`, principal.User.ID, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	items := make([]visitOut, 0, 8)
	companies := map[string]struct{}{}
	for rows.Next() {
		var item visitOut
		var note pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&item.ProjectID, &item.CompanyName, &item.ProjectTitle, &note,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Note = textPtr(note)
		item.CreatedAt = formatTime(createdAt)
		companies[item.CompanyName] = struct{}{}
		items = append(items, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, passportOut{
		Items:    items,
		Needed:   passportNeeded,
		Complete: len(companies) >= passportNeeded,
	})
}

type visitIn struct {
	Note *string `json:"note"`
}

func (s *Server) stamp(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in visitIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Note != nil && len([]rune(*in.Note)) > 280 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	projectID := r.PathValue("project_id")
	var companyID, status string
	err := s.writePool().QueryRow(ctx,
		`SELECT company_id, status FROM projects WHERE id = $1`, projectID).
		Scan(&companyID, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "published") {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在或尚未发布")
		return
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var member bool
	if err := s.writePool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2)`,
		companyID, principal.User.ID).Scan(&member); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if member {
		writeError(w, r, http.StatusForbidden, "forbidden", "不能给自己公司的作品打卡")
		return
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var visitID string
	err = s.writePool().QueryRow(ctx, `
		SELECT id FROM visits WHERE event_id = $1 AND project_id = $2 AND visitor_id = $3`,
		eventID, projectID, principal.User.ID).Scan(&visitID)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		now := time.Now().UTC()
		if _, err := s.writePool().Exec(ctx, `
			INSERT INTO visits (id, event_id, project_id, visitor_id, note, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			id, eventID, projectID, principal.User.ID, in.Note, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else if err != nil {
		s.writeQueryError(w, r, err)
		return
	} else {
		if _, err := s.writePool().Exec(ctx,
			`UPDATE visits SET note = $2, updated_at = $3 WHERE id = $1`,
			visitID, in.Note, time.Now().UTC()); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) removeStamp(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		DELETE FROM visits WHERE project_id = $1 AND visitor_id = $2`,
		r.PathValue("project_id"), principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type stampOut struct {
	VisitorName string  `json:"visitor_name"`
	Note        *string `json:"note"`
	CreatedAt   string  `json:"created_at"`
}

func (s *Server) companyStamps(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	companyID := r.PathValue("company_id")
	if principal.User.Role != "staff" && principal.User.Role != "admin" {
		var member bool
		if err := s.writePool().QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2)`,
			companyID, principal.User.ID).Scan(&member); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if !member {
			writeError(w, r, http.StatusForbidden, "forbidden", "只有成员可以查看")
			return
		}
	}
	rows, err := s.writePool().Query(ctx, `
		SELECT u.display_name, v.note, v.created_at
		FROM visits v
		JOIN projects p ON p.id = v.project_id
		JOIN users u ON u.id = v.visitor_id
		WHERE p.company_id = $1
		ORDER BY v.created_at DESC`, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	stamps := make([]stampOut, 0, 16)
	for rows.Next() {
		var stamp stampOut
		var note pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&stamp.VisitorName, &note, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		stamp.Note = textPtr(note)
		stamp.CreatedAt = formatTime(createdAt)
		stamps = append(stamps, stamp)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, stamps)
}
