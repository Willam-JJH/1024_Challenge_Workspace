package api

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

const galleryPattern = "GET /api/v1/projects"

type projectCard struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Revision    int    `json:"revision"`
	LiveVersion *int   `json:"live_version"`
	CompanyName string `json:"company_name"`
	CompanySlug string `json:"company_slug"`
}

func (s *Server) gallery(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	// 画廊只展示正在进行的这一届。赛季收口后作品只出现在往届归档里，不在这里残留。
	eventID, live, err := s.currentLiveEventID(r.Context(), pool)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !live {
		writeJSON(w, http.StatusOK, []projectCard{})
		return
	}
	rows, err := pool.Query(r.Context(), `
		SELECT p.id, p.company_id, p.status, p.title, p.summary, p.revision,
			p.live_version, c.name, c.slug
		FROM projects p
		JOIN companies c ON c.id = p.company_id
		WHERE p.status = 'published' AND c.event_id = $1
		ORDER BY p.updated_at DESC
		LIMIT 100`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	cards := make([]projectCard, 0, 64)
	for rows.Next() {
		var card projectCard
		var liveVersion pgtype.Int4
		if err := rows.Scan(&card.ID, &card.CompanyID, &card.Status, &card.Title,
			&card.Summary, &card.Revision, &liveVersion, &card.CompanyName,
			&card.CompanySlug); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if liveVersion.Valid {
			value := int(liveVersion.Int32)
			card.LiveVersion = &value
		}
		cards = append(cards, card)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, cards)
}
