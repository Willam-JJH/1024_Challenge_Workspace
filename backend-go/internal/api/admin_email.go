package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	adminEmailBroadcastsPattern = "GET /api/v1/admin/email/broadcasts"
	adminEmailBroadcastCreate   = "POST /api/v1/admin/email/broadcasts"
	adminEmailBroadcastPattern  = "PATCH /api/v1/admin/email/broadcasts/{broadcast_id}"
	adminEmailBroadcastDelete   = "DELETE /api/v1/admin/email/broadcasts/{broadcast_id}"
	adminEmailCancelPattern     = "POST /api/v1/admin/email/broadcasts/{broadcast_id}/cancellations"
	adminEmailSchedulePattern   = "POST /api/v1/admin/email/broadcasts/{broadcast_id}/schedules"
	adminEmailTestPattern       = "POST /api/v1/admin/email/broadcasts/{broadcast_id}/tests"
	adminEmailPreviewPattern    = "POST /api/v1/admin/email/broadcasts/previews"
	adminEmailEstimatePattern   = "POST /api/v1/admin/email/audience-estimates"
	adminEmailMessagesPattern   = "GET /api/v1/admin/email/messages"
	adminEmailResendPattern     = "POST /api/v1/admin/email/messages/{message_id}/resends"
	adminEmailTemplatesPattern  = "GET /api/v1/admin/email/templates"
	adminEmailTemplatePreview   = "POST /api/v1/admin/email/template-previews"
	adminEmailSetupPattern      = "GET /api/v1/admin/email/setup"
	adminEmailQuotaPattern      = "GET /api/v1/admin/email/quota"
)

var emailAudiences = map[string]bool{
	"all": true, "participants": true, "members": true, "staff": true,
}
var emailLocales = map[string]bool{"all": true, "zh": true, "en": true}

type emailTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var emailTemplates = []emailTemplate{
	{"welcome", "欢迎与激活", "账号创建后的欢迎邮件，含激活链接"},
	{"vote-reminder", "投票提醒", "投票截止前的提醒"},
	{"offline-reminder", "线下提醒", "场次开始前一天的提醒"},
	{"results", "结果公布", "入围与奖项公布通知"},
}

type broadcastOut struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	Audience    string  `json:"audience"`
	Locale      string  `json:"locale"`
	Status      string  `json:"status"`
	ScheduledAt *string `json:"scheduled_at"`
	SentAt      *string `json:"sent_at"`
}

type broadcastRow struct {
	broadcastOut
	EventID string
}

func (s *Server) broadcastByID(w http.ResponseWriter, r *http.Request,
	broadcastID string) (*broadcastRow, bool) {
	var row broadcastRow
	var scheduledAt, sentAt pgtype.Timestamp
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, title, body, audience, locale, status, scheduled_at, sent_at, event_id
		FROM broadcasts WHERE id = $1`, broadcastID).
		Scan(&row.ID, &row.Title, &row.Body, &row.Audience, &row.Locale, &row.Status,
			&scheduledAt, &sentAt, &row.EventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "广播不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.ScheduledAt = formatTimePtr(timestampPtr(scheduledAt))
	row.SentAt = formatTimePtr(timestampPtr(sentAt))
	return &row, true
}

func (s *Server) adminEmailBroadcasts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, title, body, audience, locale, status, scheduled_at, sent_at
		FROM broadcasts WHERE event_id = $1 ORDER BY created_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]broadcastOut, 0, 8)
	for rows.Next() {
		var item broadcastOut
		var scheduledAt, sentAt pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Audience, &item.Locale,
			&item.Status, &scheduledAt, &sentAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.ScheduledAt = formatTimePtr(timestampPtr(scheduledAt))
		item.SentAt = formatTimePtr(timestampPtr(sentAt))
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type broadcastIn struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Audience *string `json:"audience"`
	Locale   *string `json:"locale"`
}

func validBroadcastCreate(in broadcastIn) bool {
	if in.Title == nil || in.Body == nil {
		return false
	}
	titleLen := len([]rune(*in.Title))
	bodyLen := len([]rune(*in.Body))
	if titleLen < 1 || titleLen > 160 || bodyLen < 1 || bodyLen > 16000 {
		return false
	}
	if in.Audience != nil && !emailAudiences[*in.Audience] {
		return false
	}
	return in.Locale == nil || emailLocales[*in.Locale]
}

func validBroadcastPatch(in broadcastIn) bool {
	if in.Title != nil && len([]rune(*in.Title)) > 160 {
		return false
	}
	if in.Body != nil && len([]rune(*in.Body)) > 16000 {
		return false
	}
	if in.Audience != nil && !emailAudiences[*in.Audience] {
		return false
	}
	return in.Locale == nil || emailLocales[*in.Locale]
}

func (s *Server) adminCreateBroadcast(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in broadcastIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBroadcastCreate(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	audience := "all"
	if in.Audience != nil {
		audience = *in.Audience
	}
	locale := "all"
	if in.Locale != nil {
		locale = *in.Locale
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO broadcasts (id, event_id, title, body, audience, locale, status,
			created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $8)`,
		id, eventID, *in.Title, *in.Body, audience, locale, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, broadcastOut{
		ID:       id,
		Title:    *in.Title,
		Body:     *in.Body,
		Audience: audience,
		Locale:   locale,
		Status:   "draft",
	})
}

func (s *Server) adminPatchBroadcast(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in broadcastIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBroadcastPatch(in) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.broadcastByID(w, r, r.PathValue("broadcast_id"))
	if !ok {
		return
	}
	if row.Status == "sent" {
		writeError(w, r, http.StatusConflict, "conflict", "已发送的广播不能修改")
		return
	}
	if in.Title != nil {
		row.Title = *in.Title
	}
	if in.Body != nil {
		row.Body = *in.Body
	}
	if in.Audience != nil {
		row.Audience = *in.Audience
	}
	if in.Locale != nil {
		row.Locale = *in.Locale
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE broadcasts SET title = $2, body = $3, audience = $4, locale = $5,
			updated_at = $6 WHERE id = $1`,
		row.ID, row.Title, row.Body, row.Audience, row.Locale, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row.broadcastOut)
}

func (s *Server) adminDeleteBroadcast(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.broadcastByID(w, r, r.PathValue("broadcast_id"))
	if !ok {
		return
	}
	if row.Status != "draft" && row.Status != "cancelled" {
		writeError(w, r, http.StatusConflict, "conflict", "只能删除草稿或已取消的广播")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM broadcasts WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminCancelBroadcast(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.broadcastByID(w, r, r.PathValue("broadcast_id"))
	if !ok {
		return
	}
	if row.Status == "sent" {
		writeError(w, r, http.StatusConflict, "conflict", "已经发出去了")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE broadcasts SET status = 'cancelled', updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type emailRecipient struct {
	ID          string
	Email       string
	DisplayName string
	Locale      string
}

// emailRecipients 复刻 _recipients：活跃用户 + 受众/语言过滤。
func (s *Server) emailRecipients(ctx context.Context, audience, locale string) ([]emailRecipient, error) {
	query := `SELECT id, email, display_name, locale FROM users WHERE status = 'active'`
	switch audience {
	case "participants":
		query += ` AND role IN ('participant', 'member', 'captain')`
	case "members":
		query += ` AND id IN (SELECT user_id FROM memberships)`
	case "staff":
		query += ` AND role IN ('staff', 'admin')`
	}
	args := []any{}
	if locale != "all" {
		query += ` AND locale = $1`
		args = append(args, locale)
	}
	rows, err := s.writePool().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]emailRecipient, 0, 64)
	for rows.Next() {
		var item emailRecipient
		if err := rows.Scan(&item.ID, &item.Email, &item.DisplayName, &item.Locale); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func renderEmail(text string, user emailRecipient) string {
	text = strings.ReplaceAll(text, "{name}", user.DisplayName)
	return strings.ReplaceAll(text, "{email}", user.Email)
}

// sendEmail 复刻 _send：逐个写日志并调用驱动。
func (s *Server) sendEmail(ctx context.Context, users []emailRecipient, subject, body string,
	broadcastID *string) (int, int, error) {
	sent, failed := 0, 0
	now := time.Now().UTC()
	for _, user := range users {
		rendered := renderEmail(body, user)
		id, err := auth.NewID()
		if err != nil {
			return sent, failed, err
		}
		status := "sent"
		var sentAt any = now
		var failure any
		if err := s.mailer.SendNotice(user.Email, subject, rendered); err != nil {
			status = "failed"
			sentAt = nil
			failure = truncateError(err)
			failed++
		} else {
			sent++
		}
		if _, err := s.writePool().Exec(ctx, `
			INSERT INTO email_messages (id, broadcast_id, to_email, subject, body, status,
				error, sent_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			id, broadcastID, user.Email, subject, rendered, status, failure, sentAt,
			now); err != nil {
			return sent, failed, err
		}
	}
	return sent, failed, nil
}

// truncateError 与 Python 的 str(exc)[:500] 对齐；按 rune 截断避免切坏 UTF-8。
func truncateError(err error) string {
	text := err.Error()
	runes := []rune(text)
	if len(runes) > 500 {
		text = string(runes[:500])
	}
	return text
}

func (s *Server) adminEmailSchedule(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in struct {
		ScheduledAt *string `json:"scheduled_at"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var scheduledAt *time.Time
	if in.ScheduledAt != nil {
		parsed, ok := parseFlexibleTime(*in.ScheduledAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		scheduledAt = &parsed
	}
	row, ok := s.broadcastByID(w, r, r.PathValue("broadcast_id"))
	if !ok {
		return
	}
	if row.Status == "sent" {
		writeError(w, r, http.StatusConflict, "conflict", "已经发送过了")
		return
	}
	now := time.Now().UTC()
	if scheduledAt != nil && scheduledAt.After(now) {
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE broadcasts SET status = 'scheduled', scheduled_at = $2, updated_at = $3
			WHERE id = $1`, row.ID, *scheduledAt, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		row.Status = "scheduled"
		formatted := formatTime(*scheduledAt)
		row.ScheduledAt = &formatted
		writeJSON(w, http.StatusOK, row.broadcastOut)
		return
	}

	users, err := s.emailRecipients(r.Context(), row.Audience, row.Locale)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if len(users) == 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "这个受众没有人")
		return
	}
	if _, _, err := s.sendEmail(r.Context(), users, row.Title, row.Body, &row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE broadcasts SET status = 'sent', sent_at = $2, updated_at = $2 WHERE id = $1`,
		row.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := strconvItoa(len(users)) + " 人"
	if err := s.recordAudit(r.Context(), principal.User.ID, "email.broadcast", "broadcast",
		row.ID, &note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.Status = "sent"
	formatted := formatTime(now)
	row.SentAt = &formatted
	writeJSON(w, http.StatusOK, row.broadcastOut)
}

func (s *Server) adminEmailTestBroadcast(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	row, ok := s.broadcastByID(w, r, r.PathValue("broadcast_id"))
	if !ok {
		return
	}
	self := []emailRecipient{{
		ID:          principal.User.ID,
		Email:       principal.User.Email,
		DisplayName: principal.User.DisplayName,
		Locale:      principal.User.Locale,
	}}
	if _, _, err := s.sendEmail(r.Context(), self, "[测试] "+row.Title, row.Body, &row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type emailPreviewIn struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

func (s *Server) adminEmailPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in emailPreviewIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Title == nil || in.Body == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}{Subject: *in.Title, Body: strings.ReplaceAll(*in.Body, "{name}", "示例用户")})
}

type emailEstimateIn struct {
	Audience *string `json:"audience"`
	Locale   *string `json:"locale"`
}

func (s *Server) adminEmailEstimate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in emailEstimateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.Audience != nil && !emailAudiences[*in.Audience]) ||
		(in.Locale != nil && !emailLocales[*in.Locale]) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	audience := "all"
	if in.Audience != nil {
		audience = *in.Audience
	}
	locale := "all"
	if in.Locale != nil {
		locale = *in.Locale
	}
	users, err := s.emailRecipients(r.Context(), audience, locale)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	byLocale := map[string]int{}
	for _, user := range users {
		byLocale[user.Locale]++
	}
	writeJSON(w, http.StatusOK, struct {
		Recipients int            `json:"recipients"`
		ByLocale   map[string]int `json:"by_locale"`
	}{Recipients: len(users), ByLocale: byLocale})
}

type emailMessageOut struct {
	ID        string  `json:"id"`
	ToEmail   string  `json:"to_email"`
	Subject   string  `json:"subject"`
	Status    string  `json:"status"`
	Error     *string `json:"error"`
	SentAt    *string `json:"sent_at"`
	CreatedAt string  `json:"created_at"`
}

func (s *Server) adminEmailMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		limit = parsed
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, to_email, subject, status, error, sent_at, created_at
		FROM email_messages ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	items := make([]emailMessageOut, 0, 32)
	for rows.Next() {
		var item emailMessageOut
		var failure pgtype.Text
		var sentAt pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ToEmail, &item.Subject, &item.Status, &failure,
			&sentAt, &createdAt); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		item.Error = textPtr(failure)
		item.SentAt = formatTimePtr(timestampPtr(sentAt))
		item.CreatedAt = formatTime(createdAt)
		items = append(items, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	counts := map[string]int{"queued": 0, "sent": 0, "failed": 0}
	countRows, err := s.writePool().Query(r.Context(), `
		SELECT status, count(*) FROM email_messages GROUP BY status`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for countRows.Next() {
		var status string
		var count int
		if err := countRows.Scan(&status, &count); err != nil {
			countRows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		if _, known := counts[status]; known {
			counts[status] = count
		}
	}
	countRows.Close()
	if countRows.Err() != nil {
		s.writeQueryError(w, r, countRows.Err())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items  []emailMessageOut `json:"items"`
		Counts map[string]int    `json:"counts"`
	}{Items: items, Counts: counts})
}

func (s *Server) adminEmailResend(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	messageID := r.PathValue("message_id")
	var toEmail, subject, body, status string
	err := s.writePool().QueryRow(r.Context(), `
		SELECT to_email, subject, body, status FROM email_messages WHERE id = $1`, messageID).
		Scan(&toEmail, &subject, &body, &status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邮件不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "failed" {
		writeError(w, r, http.StatusConflict, "conflict", "只有失败的邮件可以重发")
		return
	}
	if err := s.mailer.SendNotice(toEmail, subject, body); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE email_messages SET status = 'sent', sent_at = $2, error = NULL, updated_at = $2
		WHERE id = $1`, messageID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminEmailTemplates(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, emailTemplates)
}

type templatePreviewIn struct {
	Template *string         `json:"template"`
	Locale   *string         `json:"locale"`
	Props    json.RawMessage `json:"props"`
}

type orderedProp struct {
	Key   string
	Value string
}

// orderedProps 保留 JSON 对象的键顺序（Python 的 dict 按插入顺序渲染）。
// 返回 (props, ok)：ok=false 表示字段类型不合法，需要 422。
func orderedProps(raw json.RawMessage) ([]orderedProp, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if string(raw) == "null" {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	out := make([]orderedProp, 0, 4)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		out = append(out, orderedProp{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	return out, true
}

func (s *Server) adminEmailTemplatePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in templatePreviewIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Template == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	name := ""
	for _, item := range emailTemplates {
		if item.Key == *in.Template {
			name = item.Name
			break
		}
	}
	if name == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "模板不存在")
		return
	}
	locale := "zh"
	if in.Locale != nil {
		locale = *in.Locale
	}
	props, ok := orderedProps(in.Props)
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if len(props) == 0 {
		props = []orderedProp{{Key: "name", Value: "示例用户"}}
	}
	text := renderProps(props)
	writeJSON(w, http.StatusOK, struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}{
		Subject: "[" + locale + "] " + name,
		Body:    name + "（" + locale + "）\n" + text,
	})
}

func renderProps(props []orderedProp) string {
	parts := make([]string, 0, len(props))
	for _, prop := range props {
		parts = append(parts, prop.Key+": "+prop.Value)
	}
	return strings.Join(parts, ", ")
}

func (s *Server) emailSentToday(r *http.Request) (int, error) {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var count int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT count(*) FROM email_messages WHERE sent_at >= $1`, midnight).Scan(&count)
	return count, err
}

type emailSetupOut struct {
	Driver     string `json:"driver"`
	DailyQuota *int   `json:"daily_quota"`
	SentToday  int    `json:"sent_today"`
}

func (s *Server) adminEmailSetup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	sentToday, err := s.emailSentToday(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, emailSetupOut{
		Driver:    auth.DriverName(s.mailer),
		SentToday: sentToday,
	})
}

func (s *Server) adminEmailQuota(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	sentToday, err := s.emailSentToday(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, emailSetupOut{
		Driver:    auth.DriverName(s.mailer),
		SentToday: sentToday,
	})
}
