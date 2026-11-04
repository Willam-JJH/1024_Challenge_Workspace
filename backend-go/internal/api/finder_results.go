package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	finderCompaniesPattern   = "GET /api/v1/finder/companies"
	finderPeoplePattern      = "GET /api/v1/finder/people"
	invitationPreviewPattern = "POST /api/v1/invitations/previews"
	invitationAcceptPattern  = "POST /api/v1/invitations/{invitation_id}/acceptance"
	invitationDeclinePattern = "POST /api/v1/invitations/{invitation_id}/decline"
	inviteLinkAcceptPattern  = "POST /api/v1/invite-links/{token}/acceptance"
	resultsPattern           = "GET /api/v1/results"
	shortlistPattern         = "GET /api/v1/results/shortlist"
	adminSearchPattern       = "GET /api/v1/admin/search"
)

func (s *Server) finderCompanies(w http.ResponseWriter, r *http.Request) {
	eventID, seats, found, err := s.companiesEvent(r.Context())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	rows, err := s.readPool().Query(r.Context(), `
		SELECT id, slug, name, blurb, captain_id, roster_locked
		FROM companies WHERE event_id = $1 ORDER BY created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	type entry struct {
		base companyBase
	}
	companies := make([]entry, 0, 16)
	for rows.Next() {
		var item entry
		var blurb, captain pgtype.Text
		if err := rows.Scan(&item.base.ID, &item.base.Slug, &item.base.Name, &blurb,
			&captain, &item.base.RosterLocked); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		item.base.Blurb = textPtr(blurb)
		item.base.CaptainID = textPtr(captain)
		companies = append(companies, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	counts := map[string]int{}
	ids := make([]string, 0, len(companies))
	for _, item := range companies {
		ids = append(ids, item.base.ID)
	}
	if len(ids) > 0 {
		countRows, err := s.readPool().Query(r.Context(), `
			SELECT company_id, count(*) FROM memberships
			WHERE company_id = ANY($1) GROUP BY company_id`, ids)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		for countRows.Next() {
			var companyID string
			var count int
			if err := countRows.Scan(&companyID, &count); err != nil {
				countRows.Close()
				s.writeQueryError(w, r, err)
				return
			}
			counts[companyID] = count
		}
		countRows.Close()
		if countRows.Err() != nil {
			s.writeQueryError(w, r, countRows.Err())
			return
		}
	}

	out := make([]companySummaryOut, 0, len(companies))
	for _, item := range companies {
		left := seats - counts[item.base.ID]
		if left < 0 {
			left = 0
		}
		if left <= 0 || item.base.RosterLocked {
			continue
		}
		out = append(out, companySummaryOut{
			companyBase: item.base,
			MemberCount: seats - left,
			SeatsLeft:   left,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type userLiteOut struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (s *Server) finderPeople(w http.ResponseWriter, r *http.Request) {
	eventID, _, found, err := s.companiesEvent(r.Context())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	rows, err := s.readPool().Query(r.Context(), `
		SELECT u.id, u.display_name
		FROM users u
		WHERE u.status = 'active'
			AND u.id NOT IN (
				SELECT m.user_id FROM memberships m
				JOIN companies c ON c.id = m.company_id
				WHERE c.event_id = $1
			)
		ORDER BY u.created_at
		LIMIT 100`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]userLiteOut, 0, 32)
	for rows.Next() {
		var item userLiteOut
		if err := rows.Scan(&item.ID, &item.DisplayName); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type invitationPreviewIn struct {
	Token *string `json:"token"`
}

type invitationPreviewOut struct {
	Kind        string  `json:"kind"`
	Email       *string `json:"email"`
	CompanyName string  `json:"company_name"`
	EventName   string  `json:"event_name"`
}

func (s *Server) previewInvitation(w http.ResponseWriter, r *http.Request) {
	var in invitationPreviewIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Token == nil || len(*in.Token) < 16 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	digest := auth.HashToken(*in.Token)

	var email, companyName, eventName string
	var companyID string
	err := s.writePool().QueryRow(r.Context(), `
		SELECT i.email, i.company_id, c.name, e.name
		FROM invitations i
		JOIN companies c ON c.id = i.company_id
		JOIN events e ON e.id = c.event_id
		WHERE i.token_hash = $1 LIMIT 1`, digest).
		Scan(&email, &companyID, &companyName, &eventName)
	if err == nil {
		writeJSON(w, http.StatusOK, invitationPreviewOut{
			Kind:        "invitation",
			Email:       &email,
			CompanyName: companyName,
			EventName:   eventName,
		})
		return
	}
	if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	// 邀请存在但公司或赛事缺失时，Python 返回专门的 404；JOIN 无法区分，
	// 因此再按原表查一次。
	var invitationCompany string
	err = s.writePool().QueryRow(r.Context(),
		`SELECT company_id FROM invitations WHERE token_hash = $1 LIMIT 1`, digest).
		Scan(&invitationCompany)
	if err == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请所属的公司或赛事不存在")
		return
	}
	if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}

	var disabledAt pgtype.Timestamp
	err = s.writePool().QueryRow(r.Context(), `
		SELECT l.company_id, l.disabled_at, c.name, e.name
		FROM invite_links l
		JOIN companies c ON c.id = l.company_id
		JOIN events e ON e.id = c.event_id
		WHERE l.token_hash = $1 LIMIT 1`, digest).
		Scan(&companyID, &disabledAt, &companyName, &eventName)
	if err == nil {
		if disabledAt.Valid {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请无效或已失效")
			return
		}
		writeJSON(w, http.StatusOK, invitationPreviewOut{
			Kind:        "link",
			CompanyName: companyName,
			EventName:   eventName,
		})
		return
	}
	if isNoRows(err) {
		var linkCompany string
		err = s.writePool().QueryRow(r.Context(),
			`SELECT company_id FROM invite_links WHERE token_hash = $1 LIMIT 1`, digest).
			Scan(&linkCompany)
		if err == nil {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请链接所属的公司或赛事不存在")
			return
		}
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请无效或已失效")
			return
		}
	}
	s.writeQueryError(w, r, err)
}

func (s *Server) acceptInviteLink(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	digest := auth.HashToken(r.PathValue("token"))
	var companyID string
	var disabledAt pgtype.Timestamp
	var locked bool
	err := s.writePool().QueryRow(r.Context(), `
		SELECT l.company_id, l.disabled_at, c.roster_locked
		FROM invite_links l JOIN companies c ON c.id = l.company_id
		WHERE l.token_hash = $1 LIMIT 1`, digest).
		Scan(&companyID, &disabledAt, &locked)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请链接无效或已停用")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if disabledAt.Valid {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请链接无效或已停用")
		return
	}
	if locked {
		writeError(w, r, http.StatusConflict, "conflict", "名单已锁定")
		return
	}
	var seats int
	err = s.writePool().QueryRow(r.Context(), `
		SELECT e.company_seats FROM companies c JOIN events e ON e.id = c.event_id
		WHERE c.id = $1`, companyID).Scan(&seats)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if ok, err := s.addMember(w, r, principal.User.ID, companyID, seats); err != nil {
		s.writeQueryError(w, r, err)
		return
	} else if !ok {
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) results(w http.ResponseWriter, r *http.Request) {
	var eventID string
	err := s.readPool().QueryRow(r.Context(),
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&eventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.readPool().Query(r.Context(), `
		SELECT a.id, a.kind, a.title, p.id, p.title, c.name
		FROM awards a
		JOIN projects p ON p.id = a.project_id
		JOIN companies c ON c.id = p.company_id
		WHERE a.event_id = $1 AND a.announced_at IS NOT NULL
		ORDER BY a.created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	awards := make([]awardOut, 0, 8)
	for rows.Next() {
		var item awardOut
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.ProjectID,
			&item.ProjectTitle, &item.CompanyName); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		awards = append(awards, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	board, err := s.buildBoard(r.Context(), false)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Announced bool       `json:"announced"`
		Awards    []awardOut `json:"awards"`
		Board     []boardRow `json:"board"`
	}{Announced: len(awards) > 0, Awards: awards, Board: board.Items})
}

func (s *Server) shortlist(w http.ResponseWriter, r *http.Request) {
	var eventID string
	err := s.readPool().QueryRow(r.Context(),
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&eventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.readPool().Query(r.Context(), `
		SELECT p.id, p.title, c.name, s.position
		FROM shortlist_entries s
		JOIN projects p ON p.id = s.project_id
		JOIN companies c ON c.id = p.company_id
		WHERE s.event_id = $1 AND s.announced_at IS NOT NULL
		ORDER BY s.position`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]shortlistOut, 0, 16)
	for rows.Next() {
		var item shortlistOut
		if err := rows.Scan(&item.ProjectID, &item.ProjectTitle, &item.CompanyName,
			&item.Position); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type searchHit struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

func (s *Server) adminSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	query := r.URL.Query().Get("q")
	if len([]rune(query)) < 1 || len([]rune(query)) > 120 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	pattern := "%" + strings.ToLower(query) + "%"
	hits := make([]searchHit, 0, 15)

	rows, err := s.readPool().Query(r.Context(), `
		SELECT id, display_name, email FROM users
		WHERE lower(email) LIKE $1 OR lower(display_name) LIKE $1
		LIMIT 5`, pattern)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var hit searchHit
		if err := rows.Scan(&hit.ID, &hit.Label, &hit.Hint); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		hit.Kind = "user"
		hits = append(hits, hit)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	rows, err = s.readPool().Query(r.Context(), `
		SELECT id, name, slug FROM companies WHERE lower(name) LIKE $1 LIMIT 5`, pattern)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var hit searchHit
		if err := rows.Scan(&hit.ID, &hit.Label, &hit.Hint); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		hit.Kind = "company"
		hits = append(hits, hit)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	rows, err = s.readPool().Query(r.Context(), `
		SELECT id, title, status FROM projects WHERE lower(title) LIKE $1 LIMIT 5`, pattern)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var hit searchHit
		if err := rows.Scan(&hit.ID, &hit.Label, &hit.Hint); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		hit.Kind = "project"
		hits = append(hits, hit)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	writeJSON(w, http.StatusOK, hits)
}
