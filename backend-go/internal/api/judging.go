package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	judgingProjectsPattern = "GET /api/v1/judging/projects"
	judgingScorePattern    = "PUT /api/v1/judging/projects/{project_id}/scores"
)

type scoreOut struct {
	ProjectID    string  `json:"project_id"`
	JudgeID      string  `json:"judge_id"`
	Creativity   int     `json:"creativity"`
	Engineering  int     `json:"engineering"`
	Presentation int     `json:"presentation"`
	Notes        *string `json:"notes"`
	SubmittedAt  *string `json:"submitted_at"`
}

type judgingProjectOut struct {
	Project     projectOut `json:"project"`
	CompanyName string     `json:"company_name"`
	MyScore     *scoreOut  `json:"my_score"`
}

func (s *Server) judgingProjects(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT p.id, p.company_id, p.status, p.title, p.summary, p.content, p.revision,
			p.live_version, c.name
		FROM projects p JOIN companies c ON c.id = p.company_id
		WHERE p.status = 'published'
		ORDER BY p.updated_at DESC`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	type entry struct {
		project     projectOut
		companyName string
	}
	var entries []entry
	for rows.Next() {
		var item entry
		var live pgtype.Int4
		if err := rows.Scan(&item.project.ID, &item.project.CompanyID, &item.project.Status,
			&item.project.Title, &item.project.Summary, &item.project.Content,
			&item.project.Revision, &live, &item.companyName); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		if live.Valid {
			value := int(live.Int32)
			item.project.LiveVersion = &value
		}
		entries = append(entries, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	scoreRows, err := s.writePool().Query(r.Context(), `
		SELECT project_id, judge_id, creativity, engineering, presentation, notes, submitted_at
		FROM scores WHERE judge_id = $1`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	scores := map[string]*scoreOut{}
	for scoreRows.Next() {
		var score scoreOut
		var notes pgtype.Text
		var submitted pgtype.Timestamp
		if err := scoreRows.Scan(&score.ProjectID, &score.JudgeID, &score.Creativity,
			&score.Engineering, &score.Presentation, &notes, &submitted); err != nil {
			scoreRows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		score.Notes = textPtr(notes)
		score.SubmittedAt = formatTimePtr(timestampPtr(submitted))
		scores[score.ProjectID] = &score
	}
	scoreRows.Close()
	if scoreRows.Err() != nil {
		s.writeQueryError(w, r, scoreRows.Err())
		return
	}

	out := make([]judgingProjectOut, 0, len(entries))
	for _, item := range entries {
		out = append(out, judgingProjectOut{
			Project:     item.project,
			CompanyName: item.companyName,
			MyScore:     scores[item.project.ID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type scoreIn struct {
	Creativity   int     `json:"creativity"`
	Engineering  int     `json:"engineering"`
	Presentation int     `json:"presentation"`
	Notes        *string `json:"notes"`
	Submit       bool    `json:"submit"`
}

func (s *Server) saveScore(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in scoreIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Creativity < 1 || in.Creativity > 10 || in.Engineering < 1 ||
		in.Engineering > 10 || in.Presentation < 1 || in.Presentation > 10 ||
		(in.Notes != nil && len([]rune(*in.Notes)) > 2000) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	projectID := r.PathValue("project_id")
	var status string
	err := s.writePool().QueryRow(ctx,
		`SELECT status FROM projects WHERE id = $1`, projectID).Scan(&status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "作品不存在或尚未发布")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "published" {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在或尚未发布")
		return
	}

	now := time.Now().UTC()
	var scoreID string
	var submitted pgtype.Timestamp
	err = s.writePool().QueryRow(ctx, `
		SELECT id, submitted_at FROM scores WHERE project_id = $1 AND judge_id = $2`,
		projectID, principal.User.ID).Scan(&scoreID, &submitted)
	if err != nil && !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	submittedAt := submitted
	if in.Submit {
		submittedAt = pgtype.Timestamp{Time: now, Valid: true}
	}
	if isNoRows(err) {
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := s.writePool().Exec(ctx, `
			INSERT INTO scores (id, project_id, judge_id, creativity, engineering, presentation,
				notes, submitted_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			id, projectID, principal.User.ID, in.Creativity, in.Engineering,
			in.Presentation, in.Notes, nilIfInvalid(submittedAt), now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		if _, err := s.writePool().Exec(ctx, `
			UPDATE scores SET creativity = $2, engineering = $3, presentation = $4,
				notes = $5, submitted_at = $6, updated_at = $7 WHERE id = $1`,
			scoreID, in.Creativity, in.Engineering, in.Presentation, in.Notes,
			nilIfInvalid(submittedAt), now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, scoreOut{
		ProjectID:    projectID,
		JudgeID:      principal.User.ID,
		Creativity:   in.Creativity,
		Engineering:  in.Engineering,
		Presentation: in.Presentation,
		Notes:        in.Notes,
		SubmittedAt:  formatTimePtr(timestampPtr(submittedAt)),
	})
}

func nilIfInvalid(value pgtype.Timestamp) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}
