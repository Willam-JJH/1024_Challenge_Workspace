package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	feedPattern       = "GET /api/v1/exhibition/feed"
	promotionsPattern = "GET /api/v1/exhibition/promotions"
	commentsPattern   = "GET /api/v1/exhibition/projects/{project_id}/comments"
)

type feedItem struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	CompanyName string `json:"company_name"`
	At          string `json:"at"`
}

type feedEntry struct {
	item feedItem
	at   time.Time
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	ctx := r.Context()

	projects, err := pool.Query(ctx, `
		SELECT p.title, p.updated_at, c.name
		FROM projects p
		JOIN companies c ON c.id = p.company_id
		WHERE p.status = 'published'
		ORDER BY p.updated_at DESC
		LIMIT 20`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var entries []feedEntry
	for projects.Next() {
		var title, companyName string
		var at time.Time
		if err := projects.Scan(&title, &at, &companyName); err != nil {
			projects.Close()
			s.writeQueryError(w, r, err)
			return
		}
		entries = append(entries, feedEntry{
			item: feedItem{Type: "project", Title: title, CompanyName: companyName},
			at:   at,
		})
	}
	projects.Close()
	if projects.Err() != nil {
		s.writeQueryError(w, r, projects.Err())
		return
	}

	promotions, err := pool.Query(ctx, `
		SELECT p.body, p.created_at, c.name
		FROM promotions p
		JOIN companies c ON c.id = p.company_id
		WHERE p.status = 'live'
		ORDER BY p.created_at DESC
		LIMIT 20`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for promotions.Next() {
		var body, companyName string
		var at time.Time
		if err := promotions.Scan(&body, &at, &companyName); err != nil {
			promotions.Close()
			s.writeQueryError(w, r, err)
			return
		}
		title := body
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80])
		}
		entries = append(entries, feedEntry{
			item: feedItem{Type: "promotion", Title: title, CompanyName: companyName},
			at:   at,
		})
	}
	promotions.Close()
	if promotions.Err() != nil {
		s.writeQueryError(w, r, promotions.Err())
		return
	}

	// Python 用 list.sort(reverse=True)，稳定排序：同时间保持 projects 在前的原始顺序。
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	if len(entries) > 30 {
		entries = entries[:30]
	}
	items := make([]feedItem, 0, len(entries))
	for _, entry := range entries {
		entry.item.At = formatTime(entry.at)
		items = append(items, entry.item)
	}
	writeJSON(w, http.StatusOK, items)
}

type promotionOut struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	Kind        string `json:"kind"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) listPromotions(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	rows, err := pool.Query(r.Context(), `
		SELECT p.id, c.id, c.name, p.kind, p.body, p.created_at
		FROM promotions p
		JOIN companies c ON c.id = p.company_id
		WHERE p.status = 'live' AND (p.expires_at IS NULL OR p.expires_at > $1)
		ORDER BY p.created_at DESC
		LIMIT 60`, time.Now().UTC())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	items := make([]promotionOut, 0, 16)
	for rows.Next() {
		var item promotionOut
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.CompanyID, &item.CompanyName, &item.Kind,
			&item.Body, &createdAt); err != nil {
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

type commentOut struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"project_id"`
	AuthorID   string  `json:"author_id"`
	AuthorName string  `json:"author_name"`
	ParentID   *string `json:"parent_id"`
	Body       string  `json:"body"`
	CreatedAt  string  `json:"created_at"`
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	ctx := r.Context()

	rows, err := pool.Query(ctx, `
		SELECT id, project_id, author_id, parent_id, body, created_at
		FROM comments
		WHERE project_id = $1 AND status = 'visible'
		ORDER BY created_at`, r.PathValue("project_id"))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var comments []commentOut
	authorIDs := map[string]struct{}{}
	for rows.Next() {
		var comment commentOut
		var parent pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&comment.ID, &comment.ProjectID, &comment.AuthorID, &parent,
			&comment.Body, &createdAt); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		if parent.Valid {
			value := parent.String
			comment.ParentID = &value
		}
		comment.CreatedAt = formatTime(createdAt)
		authorIDs[comment.AuthorID] = struct{}{}
		comments = append(comments, comment)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	names := map[string]string{}
	if len(authorIDs) > 0 {
		ids := make([]string, 0, len(authorIDs))
		for id := range authorIDs {
			ids = append(ids, id)
		}
		nameRows, err := pool.Query(ctx,
			`SELECT id, display_name FROM users WHERE id = ANY($1)`, ids)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		for nameRows.Next() {
			var id, name string
			if err := nameRows.Scan(&id, &name); err != nil {
				nameRows.Close()
				s.writeQueryError(w, r, err)
				return
			}
			names[id] = name
		}
		nameRows.Close()
		if nameRows.Err() != nil {
			s.writeQueryError(w, r, nameRows.Err())
			return
		}
	}

	if comments == nil {
		comments = []commentOut{}
	}
	for i := range comments {
		if name, ok := names[comments[i].AuthorID]; ok {
			comments[i].AuthorName = name
		} else {
			comments[i].AuthorName = "已注销"
		}
	}
	writeJSON(w, http.StatusOK, comments)
}
