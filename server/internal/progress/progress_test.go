package progress

import (
	"errors"
	"testing"
	"time"
)

const sample = `{
  "generatedAt": "2026-10-14",
  "title": "Подготовка к собеседованиям",
  "goal": "закрыть пробелы к senior-интервью",
  "progress": { "done": 0, "total": 37, "percent": 0 },
  "items": { "done": 25, "partial": 1, "total": 312 },
  "streak": 1,
  "lastActivity": "2026-10-14",
  "daysSinceLastActivity": 0,
  "today": "промисы и ` + "`async/await`" + `",
  "sections": [{ "id": "1.1", "title": "JavaScript core", "block": 1, "done": 7, "partial": 0, "total": 24, "completed": false }]
}`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Подготовка к собеседованиям" || *f.Goal != "закрыть пробелы к senior-интервью" ||
		f.Items.Done != 25 || f.Items.Total != 312 || f.Progress.Total != 37 || len(f.Sections) != 1 || f.Sections[0].ID != "1.1" {
		t.Fatalf("unexpected file: %+v", f)
	}
}

func TestParseRejectsNonTrack(t *testing.T) {
	for name, data := range map[string]string{
		"not json":     `<html>`,
		"other json":   `{"name":"some-package","version":"1.0.0"}`,
		"no sections":  `{"title":"x"}`,
		"bad activity": `{"title":"x","sections":[],"lastActivity":"14.10.2026"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); !errors.Is(err, ErrNotATrack) {
				t.Fatalf("err = %v, want ErrNotATrack", err)
			}
		})
	}
}

func TestStatusAt(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	at := func(day int) time.Time { return time.Date(2026, 10, day, 23, 30, 0, 0, msk) }
	last := func(s string) *string { return &s }

	cases := []struct {
		name       string
		file       File
		now        time.Time
		wantState  State
		wantStreak int
		wantDays   int // -1 — nil
	}{
		{"active today", File{Streak: 3, LastActivity: last("2026-10-14")}, at(14), StateStreak, 3, 0},
		{"streak alive yesterday", File{Streak: 3, LastActivity: last("2026-10-14")}, at(15), StateStreak, 3, 1},
		{"streak broken", File{Streak: 3, LastActivity: last("2026-10-14")}, at(16), StateGap, 0, 2},
		{"gap up to 13 days", File{Streak: 1, LastActivity: last("2026-10-01")}, at(14), StateGap, 0, 13},
		{"pause from 14 days", File{Streak: 1, LastActivity: last("2026-09-30")}, at(14), StatePause, 0, 14},
		{"new track", File{}, at(14), StateNew, 0, -1},
		{"done wins", File{Streak: 2, LastActivity: last("2026-10-14"), Progress: Counter{Done: 5, Total: 5}}, at(14), StateDone, 2, 0},
		{"generated in future", File{Streak: 1, LastActivity: last("2026-10-15")}, at(14), StateStreak, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.file.StatusAt(c.now)
			if s.State != c.wantState || s.Streak != c.wantStreak {
				t.Fatalf("got %s/%d, want %s/%d", s.State, s.Streak, c.wantState, c.wantStreak)
			}
			switch {
			case c.wantDays < 0 && s.DaysSinceLastActivity != nil:
				t.Fatalf("days = %d, want nil", *s.DaysSinceLastActivity)
			case c.wantDays >= 0 && (s.DaysSinceLastActivity == nil || *s.DaysSinceLastActivity != c.wantDays):
				t.Fatalf("days = %v, want %d", s.DaysSinceLastActivity, c.wantDays)
			}
		})
	}
}

// 23:30 по Москве — уже следующий день по UTC; «сегодня» должно считаться в поясе пользователя.
func TestStatusAtUsesUserTimezone(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	last := "2026-10-14"
	f := File{Streak: 1, LastActivity: &last}
	if d := *f.StatusAt(time.Date(2026, 10, 14, 23, 30, 0, 0, msk)).DaysSinceLastActivity; d != 0 {
		t.Fatalf("days = %d, want 0", d)
	}
	if d := *f.StatusAt(time.Date(2026, 10, 14, 23, 30, 0, 0, time.UTC).In(msk)).DaysSinceLastActivity; d != 1 {
		t.Fatalf("days = %d, want 1", d)
	}
}
