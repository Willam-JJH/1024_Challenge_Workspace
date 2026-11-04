package api

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	adminSchedulePattern           = "PATCH /api/v1/admin/event/schedule"
	adminSwitchesPattern           = "PUT /api/v1/admin/event/switches"
	adminMediaDeletePattern        = "DELETE /api/v1/admin/media/{asset_id}"
	adminFlagsPattern              = "GET /api/v1/admin/feature-flags"
	adminFlagCreatePattern         = "POST /api/v1/admin/feature-flags"
	adminFlagPattern               = "PATCH /api/v1/admin/feature-flags/{flag_id}"
	adminFlagDeletePattern         = "DELETE /api/v1/admin/feature-flags/{flag_id}"
	adminAnnouncementsPattern      = "GET /api/v1/admin/announcements"
	adminAnnouncementCreatePattern = "POST /api/v1/admin/announcements"
	adminAnnouncementPattern       = "PATCH /api/v1/admin/announcements/{announcement_id}"
	adminAnnouncementDeletePattern = "DELETE /api/v1/admin/announcements/{announcement_id}"
	adminSponsorsPattern           = "GET /api/v1/admin/sponsors"
	adminSponsorCreatePattern      = "POST /api/v1/admin/sponsors"
	adminSponsorPattern            = "PATCH /api/v1/admin/sponsors/{sponsor_id}"
	adminSponsorDeletePattern      = "DELETE /api/v1/admin/sponsors/{sponsor_id}"
	adminSponsorOrderPattern       = "PUT /api/v1/admin/sponsors/order"
)

// eventIDOr404 与 currentEventID 相同，但把“赛事未配置”映射成 Python 的 404。
func (s *Server) eventIDOr404(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := s.currentEventID(r)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return "", false
		}
		s.writeQueryError(w, r, err)
		return "", false
	}
	return id, true
}

func (s *Server) eventByID(w http.ResponseWriter, r *http.Request, eventID string) (*eventOut, bool) {
	event, err := scanEvent(s.writePool().QueryRow(r.Context(),
		`SELECT `+eventColumns+` FROM events WHERE id = $1`, eventID))
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return event, true
}

// parseFlexibleTime 接受 RFC3339（带时区）与不带时区的 ISO 写法。
func parseFlexibleTime(raw string) (time.Time, bool) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC(), true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05",
		"2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type scheduleIn struct {
	StartsAt *string `json:"starts_at"`
	EndsAt   *string `json:"ends_at"`
	Stage    *string `json:"stage"`
}

func (s *Server) adminSetSchedule(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in scheduleIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Stage != nil && len([]rune(*in.Stage)) > 32 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	var startsAt, endsAt *time.Time
	if in.StartsAt != nil {
		parsed, ok := parseFlexibleTime(*in.StartsAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		startsAt = &parsed
	}
	if in.EndsAt != nil {
		parsed, ok := parseFlexibleTime(*in.EndsAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		endsAt = &parsed
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE events SET starts_at = COALESCE($2, starts_at), ends_at = COALESCE($3, ends_at),
			stage = COALESCE($4, stage), updated_at = $5
		WHERE id = $1`, eventID, nilIfNilTime(startsAt), nilIfNilTime(endsAt), in.Stage,
		now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(r.Context(), principal.User.ID, "event.schedule", "event",
		eventID, in.Stage); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	event, ok := s.eventByID(w, r, eventID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func nilIfNilTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

type switchesIn struct {
	RegistrationOpen *bool `json:"registration_open"`
	VotingOpen       *bool `json:"voting_open"`
	PitchOnAir       *bool `json:"pitch_on_air"`
}

func (s *Server) adminSetSwitches(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in switchesIn
	if !decodeJSON(w, r, &in) {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE events SET registration_open = COALESCE($2, registration_open),
			voting_open = COALESCE($3, voting_open), pitch_on_air = COALESCE($4, pitch_on_air),
			updated_at = $5
		WHERE id = $1`, eventID, in.RegistrationOpen, in.VotingOpen, in.PitchOnAir,
		time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	event, ok := s.eventByID(w, r, eventID)
	if !ok {
		return
	}
	note := "registration=" + pyBoolText(event.RegistrationOpen) +
		" voting=" + pyBoolText(event.VotingOpen)
	if err := s.recordAudit(r.Context(), principal.User.ID, "event.switches", "event",
		eventID, &note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) adminRemoveAsset(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	assetID := r.PathValue("asset_id")
	var filename string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT filename FROM media_assets WHERE id = $1`, assetID).Scan(&filename)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "素材不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE media_assets SET status = 'removed', updated_at = $2 WHERE id = $1`,
		assetID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(r.Context(), principal.User.ID, "media.remove", "media_asset",
		assetID, &filename); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// --- 功能开关 ---------------------------------------------------------------

var flagKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,60}$`)

type featureFlagOut struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Description *string `json:"description"`
	Enabled     bool    `json:"enabled"`
}

func (s *Server) adminListFlags(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, key, description, enabled FROM feature_flags
		WHERE event_id = $1 ORDER BY key`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]featureFlagOut, 0, 8)
	for rows.Next() {
		var item featureFlagOut
		var description pgtype.Text
		if err := rows.Scan(&item.ID, &item.Key, &description, &item.Enabled); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Description = textPtr(description)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type featureFlagIn struct {
	Key         *string `json:"key"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

func (s *Server) adminCreateFlag(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in featureFlagIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Key == nil || !flagKeyRe.MatchString(*in.Key) ||
		(in.Description != nil && len([]rune(*in.Description)) > 500) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	var one int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT 1 FROM feature_flags WHERE event_id = $1 AND key = $2 LIMIT 1`,
		eventID, *in.Key).Scan(&one)
	if err == nil {
		writeError(w, r, http.StatusConflict, "conflict", "同名开关已存在")
		return
	}
	if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	enabled := false
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO feature_flags (id, event_id, key, description, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id, eventID, *in.Key, in.Description, enabled, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(r.Context(), principal.User.ID, "flag.create", "feature_flag",
		id, in.Key); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, featureFlagOut{
		ID:          id,
		Key:         *in.Key,
		Description: in.Description,
		Enabled:     enabled,
	})
}

type featureFlagPatchIn struct {
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

func (s *Server) flagByID(w http.ResponseWriter, r *http.Request,
	flagID string) (*featureFlagOut, bool) {
	var row featureFlagOut
	var description pgtype.Text
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, key, description, enabled FROM feature_flags WHERE id = $1`, flagID).
		Scan(&row.ID, &row.Key, &description, &row.Enabled)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "开关不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.Description = textPtr(description)
	return &row, true
}

func (s *Server) adminPatchFlag(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in featureFlagPatchIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Description != nil && len([]rune(*in.Description)) > 500 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.flagByID(w, r, r.PathValue("flag_id"))
	if !ok {
		return
	}
	if in.Description != nil {
		row.Description = in.Description
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE feature_flags SET description = $2, enabled = $3, updated_at = $4
		WHERE id = $1`, row.ID, row.Description, row.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) adminDeleteFlag(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.flagByID(w, r, r.PathValue("flag_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM feature_flags WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// --- 公告 -------------------------------------------------------------------

func (s *Server) adminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, title, body, status, published_at, created_at
		FROM announcements WHERE event_id = $1 ORDER BY created_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]announcementOut, 0, 8)
	for rows.Next() {
		var item announcementOut
		var publishedAt pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Status,
			&publishedAt, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.PublishedAt = formatTimePtr(timestampPtr(publishedAt))
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type announcementIn struct {
	Title   *string `json:"title"`
	Body    *string `json:"body"`
	Publish *bool   `json:"publish"`
}

func validAnnouncement(in announcementIn) bool {
	if in.Title == nil || in.Body == nil {
		return false
	}
	titleLen := len([]rune(*in.Title))
	bodyLen := len([]rune(*in.Body))
	return titleLen >= 1 && titleLen <= 160 && bodyLen >= 1 && bodyLen <= 20000
}

func (s *Server) adminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in announcementIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validAnnouncement(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	publish := in.Publish != nil && *in.Publish
	status := "draft"
	var publishedAt any
	now := time.Now().UTC()
	if publish {
		status = "published"
		publishedAt = now
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO announcements (id, event_id, title, body, status, published_at,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, eventID, *in.Title, *in.Body, status, publishedAt, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	out := announcementOut{
		ID:        id,
		Title:     *in.Title,
		Body:      *in.Body,
		Status:    status,
		CreatedAt: formatTime(now),
	}
	if publish {
		formatted := formatTime(now)
		out.PublishedAt = &formatted
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) announcementByID(w http.ResponseWriter, r *http.Request,
	id string) (*announcementOut, pgtype.Timestamp, bool) {
	var row announcementOut
	var publishedAt pgtype.Timestamp
	var createdAt time.Time
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, title, body, status, published_at, created_at
		FROM announcements WHERE id = $1`, id).
		Scan(&row.ID, &row.Title, &row.Body, &row.Status, &publishedAt, &createdAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "公告不存在")
			return nil, pgtype.Timestamp{}, false
		}
		s.writeQueryError(w, r, err)
		return nil, pgtype.Timestamp{}, false
	}
	row.PublishedAt = formatTimePtr(timestampPtr(publishedAt))
	row.CreatedAt = formatTime(createdAt)
	return &row, publishedAt, true
}

func (s *Server) adminPatchAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in announcementIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validAnnouncement(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, publishedTS, ok := s.announcementByID(w, r, r.PathValue("announcement_id"))
	if !ok {
		return
	}
	row.Title = *in.Title
	row.Body = *in.Body
	now := time.Now().UTC()
	publish := in.Publish != nil && *in.Publish
	var publishedAt any
	if publishedTS.Valid {
		publishedAt = publishedTS.Time
	}
	if publish && row.Status != "published" {
		row.Status = "published"
		formatted := formatTime(now)
		row.PublishedAt = &formatted
		publishedAt = now
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE announcements SET title = $2, body = $3, status = $4, published_at = $5,
			updated_at = $6 WHERE id = $1`,
		row.ID, row.Title, row.Body, row.Status, publishedAt, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) adminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, _, ok := s.announcementByID(w, r, r.PathValue("announcement_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM announcements WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// --- 赞助商 -----------------------------------------------------------------

func (s *Server) adminListSponsors(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, name, logo_url, website, position
		FROM sponsors WHERE event_id = $1 ORDER BY position, created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]sponsorOut, 0, 8)
	for rows.Next() {
		var item sponsorOut
		var logo, website pgtype.Text
		if err := rows.Scan(&item.ID, &item.Name, &logo, &website, &item.Position); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.LogoURL = textPtr(logo)
		item.Website = textPtr(website)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type sponsorIn struct {
	Name    *string `json:"name"`
	LogoURL *string `json:"logo_url"`
	Website *string `json:"website"`
}

func validSponsor(in sponsorIn) bool {
	if in.Name == nil {
		return false
	}
	nameLen := len([]rune(*in.Name))
	return nameLen >= 1 && nameLen <= 120 &&
		(in.LogoURL == nil || len([]rune(*in.LogoURL)) <= 500) &&
		(in.Website == nil || len([]rune(*in.Website)) <= 500)
}

func (s *Server) adminCreateSponsor(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in sponsorIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validSponsor(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	var maxPosition int
	if err := s.writePool().QueryRow(r.Context(), `
		SELECT COALESCE(max(position), 0) FROM sponsors WHERE event_id = $1`, eventID).
		Scan(&maxPosition); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	position := maxPosition + 1
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO sponsors (id, event_id, name, logo_url, website, position, created_at,
			updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, eventID, *in.Name, in.LogoURL, in.Website, position, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sponsorOut{
		ID:       id,
		Name:     *in.Name,
		LogoURL:  in.LogoURL,
		Website:  in.Website,
		Position: position,
	})
}

func (s *Server) sponsorByID(w http.ResponseWriter, r *http.Request,
	id string) (*sponsorOut, bool) {
	var row sponsorOut
	var logo, website pgtype.Text
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, name, logo_url, website, position FROM sponsors WHERE id = $1`, id).
		Scan(&row.ID, &row.Name, &logo, &website, &row.Position)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "赞助商不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.LogoURL = textPtr(logo)
	row.Website = textPtr(website)
	return &row, true
}

func (s *Server) adminPatchSponsor(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in sponsorIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validSponsor(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.sponsorByID(w, r, r.PathValue("sponsor_id"))
	if !ok {
		return
	}
	row.Name = *in.Name
	row.LogoURL = in.LogoURL
	row.Website = in.Website
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE sponsors SET name = $2, logo_url = $3, website = $4, updated_at = $5
		WHERE id = $1`, row.ID, row.Name, row.LogoURL, row.Website,
		time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) adminDeleteSponsor(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.sponsorByID(w, r, r.PathValue("sponsor_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM sponsors WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type sponsorOrderIn struct {
	IDs *[]string `json:"ids"`
}

func (s *Server) adminOrderSponsors(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in sponsorOrderIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.IDs == nil || len(*in.IDs) < 1 || len(*in.IDs) > 200 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	for index, sponsorID := range *in.IDs {
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE sponsors SET position = $3, updated_at = $4
			WHERE id = $1 AND event_id = $2`,
			sponsorID, eventID, index+1, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
