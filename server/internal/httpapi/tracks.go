package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/MegaRoks/dashboard/server/internal/github"
	"github.com/MegaRoks/dashboard/server/internal/progress"
	"github.com/MegaRoks/dashboard/server/internal/store"
)

// trackColors — ключи палитры треков; сами цвета задаёт фронтенд (--track-* в макете).
var trackColors = []string{"teal", "blue", "orange", "pink", "green"}

const progressFile = "progress.json"

type trackView struct {
	ID       int64              `json:"id"`
	Owner    string             `json:"owner"`
	Repo     string             `json:"repo"`
	URL      string             `json:"url"`
	Color    string             `json:"color"`
	Title    string             `json:"title,omitempty"`
	Goal     *string            `json:"goal,omitempty"`
	Today    *string            `json:"today,omitempty"`
	Progress *progress.Counter  `json:"progress,omitempty"` // по разделам
	Items    *progress.Items    `json:"items,omitempty"`    // по пунктам
	Sections []progress.Section `json:"sections,omitempty"`
	Last     *string            `json:"lastActivity,omitempty"`
	Status   *progress.Status   `json:"status,omitempty"`
	// Error — progress.json получить не удалось: not_found | not_a_track | unavailable
	Error string `json:"error,omitempty"`
}

func (s *server) listTracks(w http.ResponseWriter, r *http.Request) {
	tracks, err := s.Store.Tracks(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	views := make([]trackView, len(tracks))
	var wg sync.WaitGroup
	for i, t := range tracks {
		wg.Go(func() { views[i] = s.view(r.Context(), t) })
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, views)
}

func (s *server) view(ctx context.Context, t store.Track) trackView {
	repo := github.Repo{Owner: t.Owner, Name: t.Repo}
	f, err := s.fetchProgress(ctx, repo)
	if err != nil {
		s.Log.Warn("track progress unavailable", "repo", repo.String(), "err", err)
		return s.newView(t, nil, errorCode(err))
	}
	return s.newView(t, &f, "")
}

func (s *server) newView(t store.Track, f *progress.File, errCode string) trackView {
	repo := github.Repo{Owner: t.Owner, Name: t.Repo}
	v := trackView{ID: t.ID, Owner: t.Owner, Repo: t.Repo, URL: repo.URL(), Color: t.Color, Error: errCode}
	if f != nil {
		st := f.StatusAt(s.Now().In(s.Location))
		v.Title, v.Goal, v.Today = f.Title, f.Goal, f.Today
		v.Progress, v.Items, v.Sections, v.Last, v.Status = &f.Progress, &f.Items, f.Sections, f.LastActivity, &st
	}
	return v
}

func (s *server) fetchProgress(ctx context.Context, repo github.Repo) (progress.File, error) {
	data, err := s.GitHub.File(ctx, repo, progressFile)
	if err != nil {
		return progress.File{}, err
	}
	return progress.Parse(data)
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, github.ErrNotFound):
		return "not_found"
	case errors.Is(err, progress.ErrNotATrack):
		return "not_a_track"
	default:
		return "unavailable"
	}
}

type trackRequest struct {
	URL   string `json:"url"`
	Color string `json:"color,omitempty"`
}

// checkResult — ответ на проверку ссылки в форме «Новый трек».
type checkResult struct {
	// Status: ok | invalid_url | duplicate | not_found | not_a_track | unavailable
	Status  string  `json:"status"`
	Message string  `json:"message"`
	Owner   string  `json:"owner,omitempty"`
	Repo    string  `json:"repo,omitempty"`
	Title   string  `json:"title,omitempty"`
	Goal    *string `json:"goal,omitempty"`
}

var checkHTTPStatus = map[string]int{
	"ok":          http.StatusOK,
	"invalid_url": http.StatusBadRequest,
	"duplicate":   http.StatusConflict,
	"not_found":   http.StatusUnprocessableEntity,
	"not_a_track": http.StatusUnprocessableEntity,
	"unavailable": http.StatusBadGateway,
}

// checkRepo: ссылка разбирается → трек ещё не добавлен → репозиторий доступен токену → в нём валидный progress.json.
func (s *server) checkRepo(ctx context.Context, rawURL string) (checkResult, github.Repo, progress.File) {
	repo, ok := github.ParseRepoURL(rawURL)
	if !ok {
		return checkResult{Status: "invalid_url", Message: "Нужна ссылка вида github.com/owner/repo"}, repo, progress.File{}
	}
	res := checkResult{Owner: repo.Owner, Repo: repo.Name}
	fail := func(status, msg string, err error) (checkResult, github.Repo, progress.File) {
		if status == "unavailable" {
			s.Log.Error("track check failed", "repo", repo.String(), "err", err)
		}
		res.Status, res.Message = status, msg
		return res, repo, progress.File{}
	}

	if dup, err := s.Store.TrackByRepo(ctx, repo.Owner, repo.Name); err == nil {
		return fail("duplicate", "Этот репозиторий уже добавлен: "+dup.Owner+"/"+dup.Repo, nil)
	} else if !errors.Is(err, store.ErrNotFound) {
		return fail("unavailable", "Не удалось проверить репозиторий, попробуйте ещё раз", err)
	}

	canonical, err := s.GitHub.Repo(ctx, repo)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return fail("not_found", "Репозиторий не найден или у токена GitHub нет к нему доступа", err)
	case err != nil:
		return fail("unavailable", "GitHub недоступен, попробуйте ещё раз", err)
	}
	repo = canonical
	res.Owner, res.Repo = repo.Owner, repo.Name

	f, err := s.fetchProgress(ctx, repo)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return fail("not_a_track", "В репозитории нет progress.json — это не трек", err)
	case errors.Is(err, progress.ErrNotATrack):
		return fail("not_a_track", "progress.json в репозитории не похож на файл трека", err)
	case err != nil:
		return fail("unavailable", "GitHub недоступен, попробуйте ещё раз", err)
	}
	res.Status, res.Message, res.Title, res.Goal = "ok", "Репозиторий найден: "+repo.String(), f.Title, f.Goal
	return res, repo, f
}

func (s *server) validateTrack(w http.ResponseWriter, r *http.Request) {
	var req trackRequest
	if !decode(w, r, &req) {
		return
	}
	res, _, _ := s.checkRepo(r.Context(), req.URL)
	writeJSON(w, http.StatusOK, res)
}

func (s *server) createTrack(w http.ResponseWriter, r *http.Request) {
	var req trackRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Color != "" && !slices.Contains(trackColors, req.Color) {
		writeError(w, http.StatusBadRequest, "bad_color", "Неизвестный цвет трека")
		return
	}
	res, repo, f := s.checkRepo(r.Context(), req.URL)
	if res.Status != "ok" {
		writeJSON(w, checkHTTPStatus[res.Status], res)
		return
	}
	if req.Color == "" {
		// как в макете: следующий цвет палитры по числу треков
		existing, err := s.Store.Tracks(r.Context())
		if err != nil {
			s.internalError(w, err)
			return
		}
		req.Color = trackColors[len(existing)%len(trackColors)]
	}
	t, err := s.Store.CreateTrack(r.Context(), store.Track{Owner: repo.Owner, Repo: repo.Name, Color: req.Color})
	if errors.Is(err, store.ErrDuplicate) {
		writeJSON(w, http.StatusConflict, checkResult{Status: "duplicate", Message: "Этот репозиторий уже добавлен: " + repo.String()})
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.newView(t, &f, ""))
}

func (s *server) deleteTrack(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Трек не найден")
		return
	}
	switch err := s.Store.DeleteTrack(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Трек не найден")
	case err != nil:
		s.internalError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
