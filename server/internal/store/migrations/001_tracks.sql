-- Трек — репозиторий GitHub с progress.json. Прогресс не хранится: его отдаёт сам репозиторий.
CREATE TABLE tracks (
    id         INTEGER PRIMARY KEY,
    owner      TEXT NOT NULL COLLATE NOCASE,
    repo       TEXT NOT NULL COLLATE NOCASE,
    color      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (owner, repo)
);
