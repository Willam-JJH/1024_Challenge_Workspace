package api

import (
	"testing"
	"time"
)

func ptr[T any](value T) *T { return &value }

func TestSeasonCloseTime(t *testing.T) {
	ends := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		days int
		want time.Time
	}{
		{30, ends.Add(30 * 24 * time.Hour)},
		{0, ends},
		{-5, ends}, // 负数按 0 处理：收口不能早于结束时间
	}
	for _, tc := range cases {
		if got := seasonCloseTime(ends, tc.days); !got.Equal(tc.want) {
			t.Fatalf("seasonCloseTime(days=%d) = %v, want %v", tc.days, got, tc.want)
		}
	}
}

func TestSeasonClosed(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		endsAt     *time.Time
		archivedAt *time.Time
		days       int
		want       bool
	}{
		{"未排期的赛事不算空档期", nil, nil, 30, false},
		{"已收口即使没有结束时间也算", nil, ptr(now), 30, true},
		{"结束满 30 天", ptr(now.Add(-30 * 24 * time.Hour)), nil, 30, true},
		{"结束 29 天还不够", ptr(now.Add(-29 * 24 * time.Hour)), nil, 30, false},
		{"进行中的赛事不算", ptr(now.Add(time.Hour)), nil, 30, false},
		{"保留期设成 0 时结束即收口", ptr(now.Add(-time.Second)), nil, 0, true},
	}
	for _, tc := range cases {
		if got := seasonClosed(now, tc.endsAt, tc.archivedAt, tc.days); got != tc.want {
			t.Fatalf("%s: seasonClosed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnrollmentClosedReason(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	open := &enrollmentEvent{RegistrationOpen: true}

	if reason := enrollmentClosedReason(open, now); reason != "" {
		t.Fatalf("开放报名的赛事不应被拒：%s", reason)
	}
	if reason := enrollmentClosedReason(&enrollmentEvent{
		RegistrationOpen: true, ArchivedAt: ptr(now.Add(-time.Hour)),
	}, now); reason == "" {
		t.Fatal("已收口的赛事必须拒绝报名")
	}
	if reason := enrollmentClosedReason(&enrollmentEvent{
		RegistrationOpen: true, EndsAt: ptr(now.Add(-time.Minute)),
	}, now); reason == "" {
		t.Fatal("已结束的赛事必须拒绝报名")
	}
	if reason := enrollmentClosedReason(&enrollmentEvent{RegistrationOpen: false}, now); reason == "" {
		t.Fatal("报名关闭时必须拒绝")
	}
}

func TestEditLocked(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name           string
		endsAt         *time.Time
		archivedAt     *time.Time
		postEventEdits bool
		want           bool
	}{
		{"开通赛后编辑时永不只读", ptr(now.Add(-time.Hour)), ptr(now), true, false},
		{"开通时连收口也不锁", nil, ptr(now), true, false},
		{"关闭但还没结束", ptr(now.Add(time.Hour)), nil, false, false},
		{"关闭且已结束", ptr(now.Add(-time.Minute)), nil, false, true},
		{"正好到结束时刻就算结束", ptr(now), nil, false, true},
		{"关闭且已收口", ptr(now), ptr(now), false, true},
		{"未排期的新赛事不锁", nil, nil, false, false},
	}
	for _, tc := range cases {
		if got := editLocked(now, tc.endsAt, tc.archivedAt, tc.postEventEdits); got != tc.want {
			t.Fatalf("%s: editLocked = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnrollmentKindWhitelist(t *testing.T) {
	if !enrollmentKinds["participant"] || !enrollmentKinds["audience"] {
		t.Fatal("参赛者与观众必须都是合法身份")
	}
	if enrollmentKinds["captain"] {
		t.Fatal("报名不能直接选队长：队长由建队流程产生")
	}
}
