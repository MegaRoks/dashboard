// Package progress описывает progress.json трека и считает статус трека на текущий день.
// Формат файла задаёт scripts/progress.ts в репозитории трека (например, MegaRoks/Interview-prep).
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotATrack = errors.New("progress: not a track progress.json")

const dateLayout = "2006-01-02"

// PauseAfterDays — после стольких дней без занятий трек считается на паузе, а не простаивающим.
const PauseAfterDays = 14

type Counter struct {
	Done    int `json:"done"`
	Total   int `json:"total"`
	Percent int `json:"percent"`
}

type Items struct {
	Done    int `json:"done"`
	Partial int `json:"partial"`
	Total   int `json:"total"`
}

type Section struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Block     int    `json:"block"`
	Done      int    `json:"done"`
	Partial   int    `json:"partial"`
	Total     int    `json:"total"`
	Completed bool   `json:"completed"`
}

// File — progress.json. streak и daysSinceLastActivity в файле посчитаны на момент коммита,
// поэтому актуальный статус даёт StatusAt, а не поля файла.
type File struct {
	GeneratedAt  string    `json:"generatedAt"`
	Title        string    `json:"title"`
	Goal         *string   `json:"goal"`
	Progress     Counter   `json:"progress"`
	Items        Items     `json:"items"`
	Streak       int       `json:"streak"`
	LastActivity *string   `json:"lastActivity"`
	Today        *string   `json:"today"`
	Sections     []Section `json:"sections"`
}

func Parse(data []byte) (File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrNotATrack, err)
	}
	if f.Title == "" || f.Sections == nil {
		return File{}, fmt.Errorf("%w: title or sections are missing", ErrNotATrack)
	}
	if f.LastActivity != nil {
		if _, err := time.Parse(dateLayout, *f.LastActivity); err != nil {
			return File{}, fmt.Errorf("%w: lastActivity: %v", ErrNotATrack, err)
		}
	}
	return f, nil
}

type State string

const (
	StateNew    State = "new"    // занятий ещё не было
	StateStreak State = "streak" // занимался сегодня или вчера
	StateGap    State = "gap"    // перерыв меньше PauseAfterDays
	StatePause  State = "pause"  // перерыв PauseAfterDays и больше
	StateDone   State = "done"   // все разделы пройдены
)

type Status struct {
	State                 State `json:"state"`
	Streak                int   `json:"streak"`
	DaysSinceLastActivity *int  `json:"daysSinceLastActivity"`
}

// StatusAt считает статус на дату now (now — уже в часовом поясе пользователя).
// Серия жива, пока последнее занятие было сегодня или вчера; иначе она обнуляется.
func (f File) StatusAt(now time.Time) Status {
	var s Status
	if f.LastActivity != nil {
		last, _ := time.Parse(dateLayout, *f.LastActivity) // формат проверен в Parse
		today, _ := time.Parse(dateLayout, now.Format(dateLayout))
		days := max(0, int(today.Sub(last).Hours()/24))
		s.DaysSinceLastActivity = &days
		if days <= 1 {
			s.Streak = f.Streak
		}
	}
	switch {
	case f.Progress.Total > 0 && f.Progress.Done == f.Progress.Total:
		s.State = StateDone
	case s.DaysSinceLastActivity == nil:
		s.State = StateNew
	case s.Streak > 0:
		s.State = StateStreak
	case *s.DaysSinceLastActivity < PauseAfterDays:
		s.State = StateGap
	default:
		s.State = StatePause
	}
	return s
}
