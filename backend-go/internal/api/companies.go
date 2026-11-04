package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	companiesPattern      = "GET /api/v1/companies"
	companyGetPattern     = "GET /api/v1/companies/{company}"
	companyMembersPattern = "GET /api/v1/companies/{company_id}/members"
)

type companyBase struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	Blurb        *string `json:"blurb"`
	CaptainID    *string `json:"captain_id"`
	RosterLocked bool    `json:"roster_locked"`
}

type memberOut struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type companyDetailOut struct {
	companyBase
	Members   []memberOut `json:"members"`
	SeatsLeft int         `json:"seats_left"`
}

type companySummaryOut struct {
	companyBase
	MemberCount int `json:"member_count"`
	SeatsLeft   int `json:"seats_left"`
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func (s *Server) companiesEvent(ctx context.Context) (string, int, bool, error) {
	pool := s.readPool()
	if pool == nil {
		return "", 0, false, errInternal
	}
	var eventID string
	var seats int
	err := pool.QueryRow(ctx,
		`SELECT id, company_seats FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&eventID, &seats)
	if err != nil {
		if isNoRows(err) {
			return "", 0, false, nil
		}
		return "", 0, false, err
	}
	return eventID, seats, true, nil
}

func (s *Server) listCompanies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	eventID, seats, found, err := s.companiesEvent(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	pool := s.readPool()

	rows, err := pool.Query(ctx, `
		SELECT id, slug, name, blurb, captain_id, roster_locked
		FROM companies
		WHERE event_id = $1
		ORDER BY created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	type row struct {
		base companyBase
	}
	var companies []row
	for rows.Next() {
		var item row
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
	if len(companies) > 0 {
		ids := make([]string, 0, len(companies))
		for _, item := range companies {
			ids = append(ids, item.base.ID)
		}
		countRows, err := pool.Query(ctx, `
			SELECT company_id, count(*) FROM memberships
			WHERE company_id = ANY($1)
			GROUP BY company_id`, ids)
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
		memberCount := counts[item.base.ID]
		left := seats - memberCount
		if left < 0 {
			left = 0
		}
		if left <= 0 || item.base.RosterLocked {
			continue
		}
		out = append(out, companySummaryOut{
			companyBase: item.base,
			MemberCount: memberCount,
			SeatsLeft:   left,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getCompany(w http.ResponseWriter, r *http.Request) {
	company, ok := s.companyByRef(w, r, r.PathValue("company"))
	if !ok {
		return
	}
	s.writeCompanyDetail(w, r, company, http.StatusOK)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	members, ok := s.companyMembers(w, r, company.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) writeCompanyDetail(w http.ResponseWriter, r *http.Request,
	company companyBase, status int) {
	members, ok := s.companyMembers(w, r, company.ID)
	if !ok {
		return
	}
	seats, err := s.companySeats(r.Context(), company.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, status, companyDetailOut{
		companyBase: company,
		Members:     members,
		SeatsLeft:   seats,
	})
}

func (s *Server) companySeats(ctx context.Context, companyID string) (int, error) {
	pool := s.readPool()
	var seats int
	var memberCount int
	err := pool.QueryRow(ctx, `
		SELECT e.company_seats, (SELECT count(*) FROM memberships WHERE company_id = $1)
		FROM companies c
		JOIN events e ON e.id = c.event_id
		WHERE c.id = $1`, companyID).Scan(&seats, &memberCount)
	if err != nil {
		return 0, err
	}
	left := seats - memberCount
	if left < 0 {
		left = 0
	}
	return left, nil
}

// companyByRef 复刻 Python 的 company_by_ref：先按 id，再按 slug。
func (s *Server) companyByRef(w http.ResponseWriter, r *http.Request,
	ref string) (companyBase, bool) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return companyBase{}, false
	}
	query := `SELECT id, slug, name, blurb, captain_id, roster_locked FROM companies
		WHERE id = $1`
	company, err := scanCompany(pool.QueryRow(r.Context(), query, ref))
	if err != nil {
		s.writeQueryError(w, r, err)
		return companyBase{}, false
	}
	if company == nil {
		company, err = scanCompany(pool.QueryRow(r.Context(),
			`SELECT id, slug, name, blurb, captain_id, roster_locked FROM companies
			 WHERE slug = $1`, ref))
		if err != nil {
			s.writeQueryError(w, r, err)
			return companyBase{}, false
		}
	}
	if company == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return companyBase{}, false
	}
	return *company, true
}

func scanCompany(row interface{ Scan(dest ...any) error }) (*companyBase, error) {
	var company companyBase
	var blurb, captain pgtype.Text
	err := row.Scan(&company.ID, &company.Slug, &company.Name, &blurb, &captain,
		&company.RosterLocked)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	company.Blurb = textPtr(blurb)
	company.CaptainID = textPtr(captain)
	return &company, nil
}

func (s *Server) companyMembers(w http.ResponseWriter, r *http.Request,
	companyID string) ([]memberOut, bool) {
	rows, err := s.readPool().Query(r.Context(), `
		SELECT m.user_id, u.display_name, m.role
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.company_id = $1
		ORDER BY m.created_at`, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()

	members := make([]memberOut, 0, 8)
	for rows.Next() {
		var member memberOut
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
