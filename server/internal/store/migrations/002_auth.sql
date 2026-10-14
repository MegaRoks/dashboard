-- Сессия выдаётся после входа через Telegram (initData) или подтверждения в боте.
-- Хранится только SHA-256 токена: утечка базы не даёт готовых cookie.
CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    token_hash   BLOB NOT NULL UNIQUE,
    user_id      INTEGER NOT NULL,
    via          TEXT NOT NULL, -- telegram | browser
    user_agent   TEXT NOT NULL,
    ip           TEXT NOT NULL,
    created_at   INTEGER NOT NULL, -- unix seconds
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

-- Запрос на вход из браузера: ждёт решения в боте, затем один раз меняется на сессию.
-- secret_hash привязывает запрос к браузеру, который его начал (cookie __Host-login).
CREATE TABLE login_requests (
    id          TEXT PRIMARY KEY,
    secret_hash BLOB NOT NULL,
    code        TEXT NOT NULL,
    status      TEXT NOT NULL, -- pending | approved | denied | used
    user_agent  TEXT NOT NULL,
    ip          TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
);
CREATE INDEX login_requests_created_at ON login_requests (created_at);
