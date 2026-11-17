package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	archiveListPattern = "GET /api/v1/exhibition/archive"
	archiveGetPattern  = "GET /api/v1/exhibition/archive/{event_id}"
)

// archiveEventOut 是往届列表里的一届。
type archiveEventOut struct {
	ID         string  `json:"id"`
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	StartsAt   *string `json:"starts_at"`
	EndsAt     *string `json:"ends_at"`
	ArchivedAt *string `json:"archived_at"`
	Companies  int     `json:"companies"`
	Projects   int     `json:"projects"`
}

// archiveAwardOut 是往届作品拿到的奖。
type archiveAwardOut struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// archiveProjectOut 是往届的一件作品。
//
// 作品赛后仍可继续提交新版本，所以这里同时给两个版本号：`live_version` 是当前
// 线上版本（可能就是赛后新加的），`competition_version` 是比赛结束时（ends_at）
// 已经发布的最大版本 —— 评委当时看到的是它。两者不同就说明赛后还在改。
type archiveProjectOut struct {
	ID                 string            `json:"id"`
	CompanyID          string            `json:"company_id"`
	CompanyName        string            `json:"company_name"`
	CompanySlug        string            `json:"company_slug"`
	Title              string            `json:"title"`
	Summary            string            `json:"summary"`
	Awards             []archiveAwardOut `json:"awards"`
	LiveVersion        *int              `json:"live_version"`
	CompetitionVersion *int              `json:"competition_version"`
}

type archiveDetailOut struct {
	Event    archiveEventOut     `json:"event"`
	Projects []archiveProjectOut `json:"projects"`
}

const archiveEventColumns = `
	e.id, e.slug, e.name, e.starts_at, e.ends_at, e.archived_at,
	(SELECT count(*) FROM companies c WHERE c.event_id = e.id),
	(SELECT count(*) FROM projects p WHERE p.event_id = e.id AND p.status = 'published')`

func scanArchiveEvent(row interface{ Scan(...any) error }) (*archiveEventOut, error) {
	var event archiveEventOut
	var startsAt, endsAt, archivedAt pgtype.Timestamp
	if err := row.Scan(&event.ID, &event.Slug, &event.Name, &startsAt, &endsAt,
		&archivedAt, &event.Companies, &event.Projects); err != nil {
		return nil, err
	}
	event.StartsAt = formatTimePtr(timestampPtr(startsAt))
	event.EndsAt = formatTimePtr(timestampPtr(endsAt))
	event.ArchivedAt = formatTimePtr(timestampPtr(archivedAt))
	return &event, nil
}

// listArchivedEvents 返回全部已收口的赛事，新的在前。
//
// 只认 archived_at：stage 是工作人员手工排期用的展示字段，不参与系统判断。
func (s *Server) listArchivedEvents(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	rows, err := pool.Query(r.Context(), `
		SELECT `+archiveEventColumns+`
		FROM events e
		WHERE e.archived_at IS NOT NULL
		ORDER BY e.archived_at DESC, e.id DESC`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	events := make([]archiveEventOut, 0, 8)
	for rows.Next() {
		event, err := scanArchiveEvent(rows)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		events = append(events, *event)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// getArchivedEvent 返回一届往届赛事的全部已发布作品及其获奖情况。
func (s *Server) getArchivedEvent(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	ctx := r.Context()
	eventID := r.PathValue("event_id")

	event, err := scanArchiveEvent(pool.QueryRow(ctx, `
		SELECT `+archiveEventColumns+`
		FROM events e
		WHERE e.id = $1 AND e.archived_at IS NOT NULL`, eventID))
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "该届作品尚未公开")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}

	// 奖项先按作品归拢，作品列表只做一次查询。
	awards := make(map[string][]archiveAwardOut, 16)
	awardRows, err := pool.Query(ctx, `
		SELECT project_id, kind, title FROM awards WHERE event_id = $1
		ORDER BY created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for awardRows.Next() {
		var projectID string
		var award archiveAwardOut
		if err := awardRows.Scan(&projectID, &award.Kind, &award.Title); err != nil {
			awardRows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		awards[projectID] = append(awards[projectID], award)
	}
	awardRows.Close()
	if awardRows.Err() != nil {
		s.writeQueryError(w, r, awardRows.Err())
		return
	}

	rows, err := pool.Query(ctx, `
		SELECT p.id, p.company_id, c.name, c.slug, p.title, p.summary, p.live_version,
			(SELECT v.version FROM project_versions v
				WHERE v.project_id = p.id AND v.published_at <= e.ends_at
				ORDER BY v.version DESC LIMIT 1)
		FROM projects p
		JOIN companies c ON c.id = p.company_id
		JOIN events e ON e.id = p.event_id
		WHERE p.event_id = $1 AND p.status = 'published'
		ORDER BY p.updated_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	projects := make([]archiveProjectOut, 0, 16)
	for rows.Next() {
		var project archiveProjectOut
		var liveVersion, competitionVersion pgtype.Int4
		if err := rows.Scan(&project.ID, &project.CompanyID, &project.CompanyName,
			&project.CompanySlug, &project.Title, &project.Summary, &liveVersion,
			&competitionVersion); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if liveVersion.Valid {
			value := int(liveVersion.Int32)
			project.LiveVersion = &value
		}
		if competitionVersion.Valid {
			value := int(competitionVersion.Int32)
			project.CompetitionVersion = &value
		}
		project.Awards = awards[project.ID]
		if project.Awards == nil {
			// 空数组而不是 nil：前端直接读 awards.length，null 会直接抛异常。
			project.Awards = []archiveAwardOut{}
		}
		projects = append(projects, project)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	writeJSON(w, http.StatusOK, archiveDetailOut{Event: *event, Projects: projects})
}

// currentLiveEventID 返回当期赛事，且只在它还没收口时返回 true。
//
// 画廊与实时 feed 靠它区分「正在进行的这一届」和「往届」：收口之后作品只出现在
// 归档接口里，不再混进正在进行的页面。
func (s *Server) currentLiveEventID(ctx context.Context, pool queryer) (string, bool, error) {
	var id string
	var archivedAt pgtype.Timestamp
	err := pool.QueryRow(ctx, `SELECT id, archived_at FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&id, &archivedAt)
	if err != nil {
		if isNoRows(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if archivedAt.Valid {
		return "", false, nil
	}
	return id, true, nil
}
