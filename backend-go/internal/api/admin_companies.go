package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	adminCompaniesPattern       = "GET /api/v1/admin/companies"
	adminCompanyPattern         = "GET /api/v1/admin/companies/{company_id}"
	adminDissolvePattern        = "POST /api/v1/admin/companies/{company_id}/dissolutions"
	adminRosterLockPattern      = "PUT /api/v1/admin/companies/{company_id}/roster-lock"
	adminCaptainPattern         = "POST /api/v1/admin/companies/{company_id}/captain-transfers"
	adminSubmitterPattern       = "POST /api/v1/admin/companies/{company_id}/submitter-appointments"
	adminMemberMovePattern      = "POST /api/v1/admin/members/moves"
	adminProjectsPattern        = "GET /api/v1/admin/projects"
	adminProjectPattern         = "GET /api/v1/admin/projects/{project_id}"
	adminProjectVersionsPattern = "GET /api/v1/admin/projects/{project_id}/versions"
	adminProjectVersionPattern  = "GET /api/v1/admin/projects/{project_id}/versions/{version}"
)

type adminCompanyMember struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type adminCompanyProjectRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type adminCompanyRow struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Slug         string                  `json:"slug"`
	CaptainID    *string                 `json:"captain_id"`
	RosterLocked bool                    `json:"roster_locked"`
	Members      []adminCompanyMember    `json:"members"`
	Project      *adminCompanyProjectRef `json:"project"`
}

type adminCompanyDetail struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Slug         string               `json:"slug"`
	Blurb        *string              `json:"blurb"`
	CaptainID    *string              `json:"captain_id"`
	RosterLocked bool                 `json:"roster_locked"`
	Members      []adminCompanyMember `json:"members"`
	Project      *projectOut          `json:"project"`
}

type adminCompanyBase struct {
	ID           string
	Slug         string
	Name         string
	Blurb        *string
	CaptainID    *string
	RosterLocked bool
}

func (s *Server) adminCompanies(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		if err == errNoCurrentEvent {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	companies, ok := s.adminCompanyBases(w, r, eventID)
	if !ok {
		return
	}
	members, ok := s.adminMembersByCompany(w, r, eventID)
	if !ok {
		return
	}
	projects, ok := s.adminProjectRefs(w, r, eventID)
	if !ok {
		return
	}
	out := make([]adminCompanyRow, 0, len(companies))
	for _, company := range companies {
		row := adminCompanyRow{
			ID:           company.ID,
			Name:         company.Name,
			Slug:         company.Slug,
			CaptainID:    company.CaptainID,
			RosterLocked: company.RosterLocked,
			Members:      members[company.ID],
		}
		if row.Members == nil {
			row.Members = []adminCompanyMember{}
		}
		row.Project = projects[company.ID]
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCompanyBases(w http.ResponseWriter, r *http.Request,
	eventID string) ([]adminCompanyBase, bool) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, slug, name, blurb, captain_id, roster_locked
		FROM companies WHERE event_id = $1 ORDER BY created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()
	out := make([]adminCompanyBase, 0, 64)
	for rows.Next() {
		var item adminCompanyBase
		var blurb, captain pgtype.Text
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &blurb, &captain,
			&item.RosterLocked); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		item.Blurb = textPtr(blurb)
		item.CaptainID = textPtr(captain)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return out, true
}

func (s *Server) adminMembersByCompany(w http.ResponseWriter, r *http.Request,
	eventID string) (map[string][]adminCompanyMember, bool) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT m.company_id, u.id, u.display_name, m.role
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		JOIN companies c ON c.id = m.company_id
		WHERE c.event_id = $1
		ORDER BY m.created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()
	out := map[string][]adminCompanyMember{}
	for rows.Next() {
		var companyID string
		var member adminCompanyMember
		if err := rows.Scan(&companyID, &member.UserID, &member.DisplayName,
			&member.Role); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		out[companyID] = append(out[companyID], member)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return out, true
}

func (s *Server) adminProjectRefs(w http.ResponseWriter, r *http.Request,
	eventID string) (map[string]*adminCompanyProjectRef, bool) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT company_id, id, title, status FROM projects WHERE event_id = $1`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()
	out := map[string]*adminCompanyProjectRef{}
	for rows.Next() {
		var companyID string
		var ref adminCompanyProjectRef
		if err := rows.Scan(&companyID, &ref.ID, &ref.Title, &ref.Status); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		copied := ref
		out[companyID] = &copied
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return out, true
}

func (s *Server) adminCompanyByID(ctx context.Context, companyID string) (*adminCompanyBase, error) {
	var item adminCompanyBase
	var blurb, captain pgtype.Text
	err := s.writePool().QueryRow(ctx, `
		SELECT id, slug, name, blurb, captain_id, roster_locked
		FROM companies WHERE id = $1`, companyID).
		Scan(&item.ID, &item.Slug, &item.Name, &blurb, &captain, &item.RosterLocked)
	if err != nil {
		return nil, err
	}
	item.Blurb = textPtr(blurb)
	item.CaptainID = textPtr(captain)
	return &item, nil
}

func (s *Server) adminGetCompany(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	ctx := r.Context()
	companyID := r.PathValue("company_id")
	company, err := s.adminCompanyByID(ctx, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if company == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return
	}
	members, ok := s.adminMembersFor(w, r, companyID)
	if !ok {
		return
	}
	var project *projectOut
	var projectID, status, title, summary, content string
	var revision int
	var live pgtype.Int4
	err = s.writePool().QueryRow(ctx, `
		SELECT id, status, title, summary, content, revision, live_version
		FROM projects WHERE company_id = $1`, companyID).
		Scan(&projectID, &status, &title, &summary, &content, &revision, &live)
	if err == nil {
		item := projectOut{
			ID:        projectID,
			CompanyID: companyID,
			Status:    status,
			Title:     title,
			Summary:   summary,
			Content:   content,
			Revision:  revision,
		}
		if live.Valid {
			value := int(live.Int32)
			item.LiveVersion = &value
		}
		project = &item
	} else if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminCompanyDetail{
		ID:           company.ID,
		Name:         company.Name,
		Slug:         company.Slug,
		Blurb:        company.Blurb,
		CaptainID:    company.CaptainID,
		RosterLocked: company.RosterLocked,
		Members:      members,
		Project:      project,
	})
}

func (s *Server) adminMembersFor(w http.ResponseWriter, r *http.Request,
	companyID string) ([]adminCompanyMember, bool) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT u.id, u.display_name, m.role
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.company_id = $1`, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()
	members := make([]adminCompanyMember, 0, 8)
	for rows.Next() {
		var member adminCompanyMember
		if err := rows.Scan(&member.UserID, &member.DisplayName, &member.Role); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		members = append(members, member)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return members, true
}

func (s *Server) adminDissolve(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	companyID := r.PathValue("company_id")
	company, err := s.adminCompanyByID(ctx, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if company == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return
	}
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE company_id = $1`, companyID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET status = 'draft', updated_at = $2 WHERE company_id = $1`,
		companyID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE companies SET roster_locked = true, updated_at = $2 WHERE id = $1`,
		companyID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := company.Name
	if err := s.recordAudit(ctx, principal.User.ID, "company.dissolve", "company", companyID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type rosterLockIn struct {
	Locked bool `json:"locked"`
}

func (s *Server) adminRosterLock(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in rosterLockIn
	if !decodeJSON(w, r, &in) {
		return
	}
	companyID := r.PathValue("company_id")
	if !s.adminCompanyExists(w, r, companyID) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE companies SET roster_locked = $2, updated_at = $3 WHERE id = $1`,
		companyID, in.Locked, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminCompanyExists(w http.ResponseWriter, r *http.Request,
	companyID string) bool {
	var exists bool
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`, companyID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return false
	}
	return true
}

type transferIn struct {
	UserID string `json:"user_id"`
}

func (s *Server) adminTransferCaptain(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in transferIn
	if !decodeJSON(w, r, &in) {
		return
	}
	companyID := r.PathValue("company_id")
	if !s.adminCompanyExists(w, r, companyID) {
		return
	}
	if !s.adminIsMember(w, r, companyID, in.UserID) {
		return
	}
	ctx := r.Context()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE memberships SET role = 'member', updated_at = $2
		WHERE company_id = $1 AND role = 'captain'`, companyID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE memberships SET role = 'captain', updated_at = $3
		WHERE company_id = $1 AND user_id = $2`, companyID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE companies SET captain_id = $2, updated_at = $3 WHERE id = $1`,
		companyID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := in.UserID
	if err := s.recordAudit(ctx, principal.User.ID, "company.captain", "company", companyID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminAppointSubmitter(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in transferIn
	if !decodeJSON(w, r, &in) {
		return
	}
	companyID := r.PathValue("company_id")
	if !s.adminCompanyExists(w, r, companyID) {
		return
	}
	if !s.adminIsMember(w, r, companyID, in.UserID) {
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		UPDATE memberships SET role = 'member', updated_at = $2
		WHERE company_id = $1 AND role = 'submitter'`, companyID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(ctx, `
		UPDATE memberships SET role = 'submitter', updated_at = $3
		WHERE company_id = $1 AND user_id = $2`, companyID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminIsMember(w http.ResponseWriter, r *http.Request,
	companyID, userID string) bool {
	var member bool
	if err := s.writePool().QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2)`,
		companyID, userID).Scan(&member); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if !member {
		writeError(w, r, http.StatusNotFound, "not_found", "对方不是公司成员")
		return false
	}
	return true
}

type moveIn struct {
	UserID        string  `json:"user_id"`
	FromCompanyID string  `json:"from_company_id"`
	ToCompanyID   *string `json:"to_company_id"`
}

func (s *Server) adminMoveMember(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in moveIn
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	var exists bool
	if err := s.writePool().QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, in.UserID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
		return
	}
	var member bool
	if err := s.writePool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2)`,
		in.FromCompanyID, in.UserID).Scan(&member); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !member {
		writeError(w, r, http.StatusNotFound, "not_found", "该账号不在原公司")
		return
	}
	now := time.Now().UTC()
	if in.ToCompanyID != nil && *in.ToCompanyID != "" {
		if !s.adminCompanyExists(w, r, *in.ToCompanyID) {
			return
		}
		var seats, taken int
		err := s.writePool().QueryRow(ctx, `
			SELECT e.company_seats, (SELECT count(*) FROM memberships WHERE company_id = c.id)
			FROM companies c JOIN events e ON e.id = c.event_id
			WHERE c.id = $1`, *in.ToCompanyID).Scan(&seats, &taken)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if seats-taken <= 0 {
			writeError(w, r, http.StatusConflict, "conflict", "目标公司人数已满")
			return
		}
		if _, err := s.writePool().Exec(ctx, `
			UPDATE memberships SET company_id = $3, role = 'member', updated_at = $4
			WHERE company_id = $1 AND user_id = $2`,
			in.FromCompanyID, in.UserID, *in.ToCompanyID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		if _, err := s.writePool().Exec(ctx, `
			DELETE FROM memberships WHERE company_id = $1 AND user_id = $2`,
			in.FromCompanyID, in.UserID); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if _, err := s.writePool().Exec(ctx,
			`UPDATE users SET role = 'participant', updated_at = $2 WHERE id = $1`,
			in.UserID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	target := "None"
	if in.ToCompanyID != nil && *in.ToCompanyID != "" {
		target = *in.ToCompanyID
	}
	note := fmt.Sprintf("%s -> %s", in.FromCompanyID, target)
	if err := s.recordAudit(ctx, principal.User.ID, "member.move", "user", in.UserID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, company_id, status, title, summary, content, revision, live_version
		FROM projects WHERE event_id = $1 ORDER BY updated_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]projectOut, 0, 64)
	for rows.Next() {
		var item projectOut
		var live pgtype.Int4
		if err := rows.Scan(&item.ID, &item.CompanyID, &item.Status, &item.Title, &item.Summary,
			&item.Content, &item.Revision, &live); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if live.Valid {
			value := int(live.Int32)
			item.LiveVersion = &value
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	project, err := s.projectByID(r.Context(), r.PathValue("project_id"))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if project == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在")
		return
	}
	writeJSON(w, http.StatusOK, project.out())
}

func (s *Server) adminProjectVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, version, title, summary, published_at
		FROM project_versions WHERE project_id = $1 ORDER BY version DESC`,
		r.PathValue("project_id"))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]versionOut, 0, 8)
	for rows.Next() {
		var version versionOut
		var publishedAt time.Time
		if err := rows.Scan(&version.ID, &version.Version, &version.Title, &version.Summary,
			&publishedAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		version.PublishedAt = formatTime(publishedAt)
		out = append(out, version)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminProjectVersion(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var version versionOut
	var publishedAt time.Time
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, version, title, summary, published_at
		FROM project_versions WHERE project_id = $1 AND version = $2::int`,
		r.PathValue("project_id"), r.PathValue("version")).
		Scan(&version.ID, &version.Version, &version.Title, &version.Summary, &publishedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "版本不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	version.PublishedAt = formatTime(publishedAt)
	writeJSON(w, http.StatusOK, version)
}
