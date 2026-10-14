# Личный дашборд

Telegram Mini App с треками обучения: прогресс, серии, шаг на сегодня, пуши от бота.
Трек — репозиторий GitHub с `progress.json` в корне (пример — `MegaRoks/Interview-prep`, файл собирает его pre-commit хук).

```
server/   бэкенд на Go: API для Mini App, SQLite
web/      Mini App: login/ — публичная страница входа; приложение на Vite + React + Grain UI — следующий шаг
```

## Доступ

Дашборд личный: всё, кроме страницы входа, видит только владелец — `TELEGRAM_ALLOWED_USER_ID`.

- **Внутри Telegram.** `/login` отправляет `initData` в `POST /auth/telegram`. Сервер проверяет подпись токеном бота,
  срок (`INITDATA_TTL`, 10 минут) и `user.id`, затем выдаёт сессию.
- **В браузере.** «Войти через Telegram» → `POST /auth/browser` → бот присылает владельцу запрос с кодом, устройством и IP,
  кнопки «Подтвердить» / «Это не я». Браузер опрашивает `GET /auth/browser/{id}` и после подтверждения один раз получает сессию.
  Запрос живёт 5 минут и привязан к браузеру cookie `__Host-login`. Не больше 3 запросов с IP и 10 всего за 10 минут.
- **Сессия.** Cookie `__Host-session` (`HttpOnly`, `Secure`, `SameSite=Lax`), 30 дней с продлением при использовании;
  в базе — только SHA-256 токена. `POST /auth/logout` — выйти, `/logout_all` в боте — выйти везде.
- **CSRF.** Запросы кроме `GET`/`HEAD` принимаются только с `Origin` = `DASHBOARD_ORIGIN`.
- **Статика.** nginx пропускает к приложению через `auth_request /auth/check`; без сессии — редирект на `/login`.
  Публичны только `/login`, `/auth/*` и `/healthz`.
- **Бот** отвечает и принимает нажатия только от владельца, в личном чате; апдейты забирает long polling.

Локально без Telegram:

```bash
curl -c jar -H "Origin: http://localhost:8090" -d "{\"initData\":\"$(make -s initdata)\"}" localhost:8090/auth/telegram
curl -b jar localhost:8090/api/tracks
```

## API

| Метод | Путь | Что делает |
|---|---|---|
| `POST` | `/auth/telegram` | `{initData}` → cookie сессии |
| `POST` | `/auth/browser` | запрос на вход в бот → `{id, code, expiresAt}` |
| `GET` | `/auth/browser/{id}` | `pending`, `ok` (+ cookie сессии), `denied`, `expired` |
| `POST` | `/auth/logout` | завершить текущую сессию |
| `GET` | `/auth/check` | для nginx: `204` есть сессия, `401` нет |
| `GET` | `/api/me` | владелец, способ входа, срок сессии, часовой пояс |
| `GET` | `/api/tracks` | треки с прогрессом из `progress.json` и статусом на сегодня |
| `POST` | `/api/tracks/validate` | `{url}` → `ok`, `invalid_url`, `duplicate`, `not_found`, `not_a_track`, `unavailable` |
| `POST` | `/api/tracks` | `{url, color?}` — добавить трек; `color`: `teal`, `blue`, `orange`, `pink`, `green` |
| `DELETE` | `/api/tracks/{id}` | удалить трек |

Статус трека (`status.state`): `new` — занятий не было, `streak` — занимался сегодня или вчера,
`gap` — перерыв меньше 14 дней, `pause` — 14 дней и больше, `done` — все разделы пройдены.
Серия и «дней назад» пересчитываются на текущую дату: `progress.json` обновляется только при коммите.
