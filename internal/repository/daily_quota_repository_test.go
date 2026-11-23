package repository

import (
	"errors"
	"testing"
	"time"
)

// Today 必须按业务时区切日，而不是按服务器本地时区。
//
// 场景：容器是 UTC，北京时间 2026-11-20 07:00 时
// UTC 还是 2026-11-19 23:00。如果直接取 UTC 日期，
// 额度会在北京时间早上 8 点才重置，用户会觉得"次数没刷新"。
func TestTodayUsesBusinessTimezone(t *testing.T) {
	cst := time.FixedZone("CST", 8*60*60)
	r := &DailyQuotaRepository{loc: cst}

	utcMoment := time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC)
	got := r.Today(utcMoment)

	want := time.Date(2026, 11, 20, 0, 0, 0, 0, cst)
	if !got.Equal(want) {
		t.Errorf("Today = %v, want %v (北京时间已是 20 日 07:00)", got, want)
	}
}

// 时区必须落在返回的日期上，否则后续拼 SQL 会带错偏移。
func TestTodayKeepsLocation(t *testing.T) {
	cst := time.FixedZone("CST", 8*60*60)
	r := &DailyQuotaRepository{loc: cst}

	got := r.Today(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	if got.Location() != cst {
		t.Errorf("location = %v, want %v", got.Location(), cst)
	}
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
		t.Errorf("date should be truncated to midnight, got %v", got)
	}
}

// loc 为 nil 时不能 panic：构建时忘了传时区也要能跑起来。
func TestNewDailyQuotaRepositoryDefaultsToUTC(t *testing.T) {
	r := NewDailyQuotaRepository(nil, nil)
	if r == nil {
		t.Fatal("repository should not be nil")
	}
	if r.loc != time.UTC {
		t.Errorf("loc = %v, want UTC", r.loc)
	}
}

// 非法的 userID / limit 直接判超额，不落库。
// 这类调用只可能来自代码 bug，不该给数据库留垃圾行。
func TestConsumeRejectsInvalidInput(t *testing.T) {
	r := &DailyQuotaRepository{loc: time.UTC}

	cases := []struct {
		name   string
		userID int64
		limit  int
	}{
		{"userID 为 0", 0, 10},
		{"userID 为负", -1, 10},
		{"limit 为 0", 1, 0},
		{"limit 为负", 1, -1},
	}

	for _, c := range cases {
		_, err := r.Consume(nil, c.userID, QuotaFaceAnalysis, c.limit, time.Now())
		if !errors.Is(err, ErrQuotaExceeded) {
			t.Errorf("%s: expected ErrQuotaExceeded, got %v", c.name, err)
		}
	}
}
