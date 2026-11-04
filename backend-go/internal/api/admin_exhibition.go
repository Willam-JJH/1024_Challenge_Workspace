package api

import (
	"math/rand"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	adminExhibitionPattern           = "GET /api/v1/admin/exhibition"
	adminAssignmentsPattern          = "GET /api/v1/admin/exhibition/assignments"
	adminAssignmentPattern           = "PATCH /api/v1/admin/exhibition/assignments/{assignment_id}"
	adminAssignmentCancelPattern     = "POST /api/v1/admin/exhibition/assignments/{assignment_id}/cancellations"
	adminAssignmentGivenPattern      = "POST /api/v1/admin/exhibition/assignments/{assignment_id}/given"
	adminAssignmentReassignPattern   = "POST /api/v1/admin/exhibition/assignments/{assignment_id}/reassignments"
	adminAssignmentsRemainingPattern = "POST /api/v1/admin/exhibition/assignments/remaining"
	adminAssignmentsGeneratePattern  = "POST /api/v1/admin/exhibition/assignments/generation"
	adminAssignmentsPublishPattern   = "POST /api/v1/admin/exhibition/assignments/publications"
	adminAssignmentsResetPattern     = "POST /api/v1/admin/exhibition/assignments/reset"
	adminAssignmentsRevealPattern    = "POST /api/v1/admin/exhibition/assignments/reveal-all"
	adminExhibitionCollabPattern     = "GET /api/v1/admin/exhibition/collaborations"
	adminExhibitionCommentsPattern   = "GET /api/v1/admin/exhibition/comments"
	adminIntroModeratePattern        = "POST /api/v1/admin/exhibition/introductions/{introduction_id}/moderation"
	adminReplyModeratePattern        = "POST /api/v1/admin/exhibition/replies/{reply_id}/moderation"
	adminPresencePattern             = "PUT /api/v1/admin/exhibition/presence/{company_id}"
	adminExhibitionPromotionsPattern = "GET /api/v1/admin/exhibition/promotions"
)

type exhibitionOverviewOut struct {
	Companies              int `json:"companies"`
	PublishedIntroductions int `json:"published_introductions"`
	DraftIntroductions     int `json:"draft_introductions"`
	Comments               int `json:"comments"`
	ReportedComments       int `json:"reported_comments"`
	Visits                 int `json:"visits"`
	LivePromotions         int `json:"live_promotions"`
}

func (s *Server) adminExhibitionOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	var out exhibitionOverviewOut
	err := s.writePool().QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM companies WHERE event_id = $1),
			(SELECT count(*) FROM introductions WHERE event_id = $1 AND status = 'published'),
			(SELECT count(*) FROM introductions WHERE event_id = $1 AND status = 'draft'),
			(SELECT count(*) FROM comments),
			(SELECT count(DISTINCT comment_id) FROM comment_reports),
			(SELECT count(*) FROM visits WHERE event_id = $1),
			(SELECT count(*) FROM promotions WHERE status = 'live')`, eventID).
		Scan(&out.Companies, &out.PublishedIntroductions, &out.DraftIntroductions,
			&out.Comments, &out.ReportedComments, &out.Visits, &out.LivePromotions)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type assignmentOut struct {
	ID                string  `json:"id"`
	IntroducerCompany string  `json:"introducer_company"`
	TargetCompany     string  `json:"target_company"`
	Status            string  `json:"status"`
	GivenAt           *string `json:"given_at"`
	HasText           bool    `json:"has_text"`
	HasReply          bool    `json:"has_reply"`
}

type adminIntroRow struct {
	ID           string
	IntroducerID string
	TargetID     string
	Text         pgtype.Text
	Reply        pgtype.Text
	ReplyStatus  string
	Status       string
	GivenAt      pgtype.Timestamp
	EventID      string
}

func (s *Server) companyNameByID(r *http.Request, companyID string) (string, error) {
	var name string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT name FROM companies WHERE id = $1`, companyID).Scan(&name)
	if err != nil {
		if isNoRows(err) {
			return "", nil
		}
		return "", err
	}
	return name, nil
}

func (s *Server) assignmentOut(w http.ResponseWriter, r *http.Request,
	row *adminIntroRow) (*assignmentOut, bool) {
	introducer, err := s.companyNameByID(r, row.IntroducerID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	target, err := s.companyNameByID(r, row.TargetID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &assignmentOut{
		ID:                row.ID,
		IntroducerCompany: introducer,
		TargetCompany:     target,
		Status:            row.Status,
		GivenAt:           formatTimePtr(timestampPtr(row.GivenAt)),
		HasText:           row.Text.Valid && row.Text.String != "",
		HasReply:          row.Reply.Valid && row.Reply.String != "",
	}, true
}

func (s *Server) adminIntroByID(w http.ResponseWriter, r *http.Request,
	introID string) (*adminIntroRow, bool) {
	var row adminIntroRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, introducer_company_id, target_company_id, text, reply, reply_status,
			status, given_at, event_id
		FROM introductions WHERE id = $1`, introID).
		Scan(&row.ID, &row.IntroducerID, &row.TargetID, &row.Text, &row.Reply,
			&row.ReplyStatus, &row.Status, &row.GivenAt, &row.EventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "介绍任务不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func (s *Server) adminAssignments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, introducer_company_id, target_company_id, text, reply, reply_status,
			status, given_at, event_id
		FROM introductions WHERE event_id = $1 ORDER BY created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	collected := make([]adminIntroRow, 0, 32)
	for rows.Next() {
		var row adminIntroRow
		if err := rows.Scan(&row.ID, &row.IntroducerID, &row.TargetID, &row.Text, &row.Reply,
			&row.ReplyStatus, &row.Status, &row.GivenAt, &row.EventID); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		collected = append(collected, row)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	out := make([]assignmentOut, 0, len(collected))
	for index := range collected {
		item, ok := s.assignmentOut(w, r, &collected[index])
		if !ok {
			return
		}
		out = append(out, *item)
	}
	writeJSON(w, http.StatusOK, out)
}

type assignTargetIn struct {
	TargetCompanyID *string `json:"target_company_id"`
}

func (s *Server) adminPatchAssignment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in assignTargetIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.TargetCompanyID == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("assignment_id"))
	if !ok {
		return
	}
	if row.Status != "draft" {
		writeError(w, r, http.StatusConflict, "conflict", "只能调整草稿")
		return
	}
	exists, err := s.companyExists(r, *in.TargetCompanyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "目标公司不存在")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET target_company_id = $2, updated_at = $3 WHERE id = $1`,
		row.ID, *in.TargetCompanyID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.TargetID = *in.TargetCompanyID
	item, ok := s.assignmentOut(w, r, row)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) companyExists(r *http.Request, companyID string) (bool, error) {
	var one int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT 1 FROM companies WHERE id = $1`, companyID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Server) adminCancelAssignment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("assignment_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET status = 'cancelled', updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type givenIn struct {
	Given *bool `json:"given"`
}

func (s *Server) adminMarkGiven(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in givenIn
	if !decodeJSON(w, r, &in) {
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("assignment_id"))
	if !ok {
		return
	}
	given := true
	if in.Given != nil {
		given = *in.Given
	}
	var value any
	if given {
		value = time.Now().UTC()
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET given_at = $2, updated_at = $3 WHERE id = $1`,
		row.ID, value, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminReassign(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in assignTargetIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.TargetCompanyID == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("assignment_id"))
	if !ok {
		return
	}
	exists, err := s.companyExists(r, *in.TargetCompanyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "目标公司不存在")
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET target_company_id = $2, text = NULL, reply = NULL,
			status = 'draft', updated_at = $3 WHERE id = $1`,
		row.ID, *in.TargetCompanyID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.TargetID = *in.TargetCompanyID
	row.Text = pgtype.Text{}
	row.Reply = pgtype.Text{}
	row.Status = "draft"
	item, ok := s.assignmentOut(w, r, row)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

type companyRef struct {
	ID   string
	Name string
}

func (s *Server) orderedCompanies(r *http.Request, eventID string) ([]companyRef, error) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, name FROM companies WHERE event_id = $1 ORDER BY created_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]companyRef, 0, 64)
	for rows.Next() {
		var item companyRef
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) insertIntroduction(r *http.Request, eventID, introducerID,
	targetID string) (string, error) {
	id, err := auth.NewID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(r.Context(), `
		INSERT INTO introductions (id, event_id, introducer_company_id, target_company_id,
			reply_status, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'visible', 'draft', $5, $5)`,
		id, eventID, introducerID, targetID, now)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Server) adminAssignRemaining(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	companies, err := s.orderedCompanies(r, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	covered := map[string]bool{}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT introducer_company_id FROM introductions
		WHERE event_id = $1 AND status <> 'cancelled'`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		covered[id] = true
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	created := make([]assignmentOut, 0, len(companies))
	for _, company := range companies {
		if covered[company.ID] {
			continue
		}
		others := make([]companyRef, 0, len(companies)-1)
		for _, other := range companies {
			if other.ID != company.ID {
				others = append(others, other)
			}
		}
		if len(others) == 0 {
			break
		}
		target := others[rand.Intn(len(others))]
		introID, err := s.insertIntroduction(r, eventID, company.ID, target.ID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		created = append(created, assignmentOut{
			ID:                introID,
			IntroducerCompany: company.Name,
			TargetCompany:     target.Name,
			Status:            "draft",
		})
	}
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) adminGenerateAssignments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	companies, err := s.orderedCompanies(r, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if len(companies) < 2 {
		writeError(w, r, http.StatusConflict, "conflict", "至少需要两家公司")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM introductions WHERE event_id = $1`, eventID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rand.Shuffle(len(companies), func(i, j int) {
		companies[i], companies[j] = companies[j], companies[i]
	})
	created := make([]assignmentOut, 0, len(companies))
	for index, company := range companies {
		target := companies[(index+1)%len(companies)]
		introID, err := s.insertIntroduction(r, eventID, company.ID, target.ID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		created = append(created, assignmentOut{
			ID:                introID,
			IntroducerCompany: company.Name,
			TargetCompany:     target.Name,
			Status:            "draft",
		})
	}
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) adminPublishAssignments(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	var count int
	if err := s.writePool().QueryRow(r.Context(), `
		SELECT count(*) FROM introductions WHERE event_id = $1 AND status = 'draft'`,
		eventID).Scan(&count); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if count == 0 {
		writeError(w, r, http.StatusConflict, "conflict", "没有待发布的草稿")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET status = 'published', updated_at = $2
		WHERE event_id = $1 AND status = 'draft'`,
		eventID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := strconvItoa(count) + " 条"
	if err := s.recordAudit(r.Context(), principal.User.ID, "exhibition.publish", "event",
		eventID, &note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminResetAssignments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM introductions WHERE event_id = $1`, eventID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminRevealAll(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET given_at = $2, updated_at = $2
		WHERE event_id = $1 AND status = 'published'`, eventID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminExhibitionCollaborations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, from_company_id, to_company_id, kind, body, status
		FROM collaborations WHERE event_id = $1 ORDER BY created_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	collected := make([]struct {
		ID     string
		FromID string
		ToID   string
		Kind   string
		Body   string
		Status string
	}, 0, 16)
	for rows.Next() {
		var item struct {
			ID     string
			FromID string
			ToID   string
			Kind   string
			Body   string
			Status string
		}
		if err := rows.Scan(&item.ID, &item.FromID, &item.ToID, &item.Kind, &item.Body,
			&item.Status); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		collected = append(collected, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	out := make([]collaborationOut, 0, len(collected))
	for _, item := range collected {
		from, err := s.companyNameByID(r, item.FromID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		to, err := s.companyNameByID(r, item.ToID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, collaborationOut{
			ID:          item.ID,
			FromCompany: from,
			ToCompany:   to,
			Kind:        item.Kind,
			Body:        item.Body,
			Status:      item.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type adminCommentOut struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	Status     string `json:"status"`
	Reports    int    `json:"reports"`
}

func (s *Server) adminExhibitionComments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	counts := map[string]int{}
	countRows, err := s.writePool().Query(r.Context(),
		`SELECT comment_id, count(*) FROM comment_reports GROUP BY comment_id`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for countRows.Next() {
		var commentID string
		var count int
		if err := countRows.Scan(&commentID, &count); err != nil {
			countRows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		counts[commentID] = count
	}
	countRows.Close()
	if countRows.Err() != nil {
		s.writeQueryError(w, r, countRows.Err())
		return
	}

	rows, err := s.writePool().Query(r.Context(), `
		SELECT c.id, c.project_id, u.display_name, c.body, c.status, c.created_at
		FROM comments c JOIN users u ON u.id = c.author_id
		ORDER BY c.created_at DESC LIMIT 300`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]adminCommentOut, 0, 64)
	for rows.Next() {
		var item adminCommentOut
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.AuthorName, &item.Body,
			&item.Status, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Reports = counts[item.ID]
		items = append(items, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	// Python 在这里按举报数稳定倒序。
	sort.SliceStable(items, func(i, j int) bool { return items[i].Reports > items[j].Reports })
	writeJSON(w, http.StatusOK, items)
}

type moderationFlagIn struct {
	Hidden *bool `json:"hidden"`
}

func (s *Server) adminModerateIntroduction(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in moderationFlagIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Hidden == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("introduction_id"))
	if !ok {
		return
	}
	status := "published"
	if *in.Hidden {
		status = "hidden"
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET status = $2, updated_at = $3 WHERE id = $1`,
		row.ID, status, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminModerateReply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in moderationFlagIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Hidden == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.adminIntroByID(w, r, r.PathValue("reply_id"))
	if !ok {
		return
	}
	status := "visible"
	if *in.Hidden {
		status = "hidden"
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE introductions SET reply_status = $2, updated_at = $3 WHERE id = $1`,
		row.ID, status, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type presenceIn struct {
	Present *bool `json:"present"`
}

func (s *Server) adminSetPresence(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in presenceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Present == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	companyID := r.PathValue("company_id")
	exists, err := s.companyExists(r, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE companies SET present = $2, updated_at = $3 WHERE id = $1`,
		companyID, *in.Present, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminExhibitionPromotions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT p.id, p.company_id, c.name, p.kind, p.body, p.created_at
		FROM promotions p JOIN companies c ON c.id = p.company_id
		ORDER BY p.created_at DESC LIMIT 200`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]promotionOut, 0, 32)
	for rows.Next() {
		var item promotionOut
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.CompanyID, &item.CompanyName, &item.Kind,
			&item.Body, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
