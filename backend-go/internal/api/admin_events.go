package api

import (
	"net/http"
	"regexp"
	"time"

	"challenge-api/internal/auth"
)

const adminEventCreatePattern = "POST /api/v1/admin/events"

// 赛事标识只能是小写字母、数字和连字符：它会出现在 URL、OAuth 回调与运维脚本里。
var eventSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,79}$`)

// 新建时只允许这些阶段。archived 不在这里：收口由赛季任务写入 archived_at，
// 手工造一个「已归档但没有归档时间」的赛事会让归档接口和判断逻辑自相矛盾。
var creatableEventStages = map[string]bool{
	"draft": true, "registration": true, "build": true,
	"judging": true, "results": true,
}

type eventCreateIn struct {
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	Stage            *string `json:"stage"`
	StartsAt         *string `json:"starts_at"`
	EndsAt           *string `json:"ends_at"`
	RegistrationOpen *bool   `json:"registration_open"`
	VotingOpen       *bool   `json:"voting_open"`
	CompanySeats     *int    `json:"company_seats"`
}

// createEvent 新建下一届赛事。
//
// 新建默认 draft + 报名与投票都关着：先把赛程排好，再由工作人员显式打开报名。
// 「当前赛事」仍然由 APP_CURRENT_EVENT_SLUG 决定，换届要改配置并重启，见部署文档。
func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in eventCreateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !eventSlugPattern.MatchString(in.Slug) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "赛事标识只能用小写字母、数字和连字符")
		return
	}
	nameRunes := []rune(in.Name)
	if len(nameRunes) == 0 || len(nameRunes) > 160 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "赛事名称不能为空且不超过 160 字")
		return
	}
	stage := "draft"
	if in.Stage != nil {
		if !creatableEventStages[*in.Stage] {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		stage = *in.Stage
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
	if startsAt != nil && endsAt != nil && !endsAt.After(*startsAt) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "结束时间必须晚于开始时间")
		return
	}
	seats := 4
	if in.CompanySeats != nil {
		if *in.CompanySeats < 1 || *in.CompanySeats > 100 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "每队人数需在 1 到 100 之间")
			return
		}
		seats = *in.CompanySeats
	}
	registrationOpen := false
	if in.RegistrationOpen != nil {
		registrationOpen = *in.RegistrationOpen
	}
	votingOpen := false
	if in.VotingOpen != nil {
		votingOpen = *in.VotingOpen
	}

	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	event, err := scanEvent(s.writePool().QueryRow(r.Context(), `
		INSERT INTO events (id, slug, name, stage, starts_at, ends_at, archived_at,
			registration_open, voting_open, post_event_edits, votes_visible,
			llm_budget_micro, llm_kill_switch, pitch_title, pitch_on_air, company_seats,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, $8, true, true, 0, false, $9, false, $10,
			$11, $11)
		RETURNING `+eventColumns,
		id, in.Slug, in.Name, stage, nilIfNilTime(startsAt), nilIfNilTime(endsAt),
		registrationOpen, votingOpen, "Pitching Day", seats, now))
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, r, http.StatusConflict, "conflict", "赛事标识已存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	note := "slug=" + in.Slug + " stage=" + stage
	if err := s.recordAudit(r.Context(), principal.User.ID, "event.create", "event", event.ID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}
