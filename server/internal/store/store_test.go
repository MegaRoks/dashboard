package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestTracks(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()

	created, err := s.CreateTrack(ctx, Track{Owner: "MegaRoks", Repo: "Interview-prep", Color: "blue"})
	if err != nil || created.ID == 0 || created.CreatedAt.IsZero() {
		t.Fatalf("CreateTrack = %+v, %v", created, err)
	}
	if _, err := s.CreateTrack(ctx, Track{Owner: "megaroks", Repo: "interview-prep", Color: "teal"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate in other case: err = %v", err)
	}
	found, err := s.TrackByRepo(ctx, "MEGAROKS", "interview-PREP")
	if err != nil || found != created {
		t.Fatalf("TrackByRepo = %+v, %v; want %+v", found, err, created)
	}
	if _, err := s.CreateTrack(ctx, Track{Owner: "MegaRoks", Repo: "grain-ui", Color: "teal"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Tracks(ctx)
	if err != nil || len(list) != 2 || list[0] != created || list[1].Repo != "grain-ui" {
		t.Fatalf("Tracks = %+v, %v", list, err)
	}

	if err := s.DeleteTrack(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTrack(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
	if _, err := s.TrackByRepo(ctx, "MegaRoks", "Interview-prep"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted track found: err = %v", err)
	}
}

// Повторное открытие не должно заново применять миграции и терять данные.
func TestReopenKeepsData(t *testing.T) {
	s, path := open(t)
	if _, err := s.CreateTrack(context.Background(), Track{Owner: "a", Repo: "b", Color: "blue"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if list, err := s2.Tracks(context.Background()); err != nil || len(list) != 1 {
		t.Fatalf("after reopen: %+v, %v", list, err)
	}
}
