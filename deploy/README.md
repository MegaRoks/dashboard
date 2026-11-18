# Деплой

При пуше в `main` workflow [`.github/workflows/deploy.yml`](../.github/workflows/deploy.yml):

1. гоняет `make lint` и `make test`;
2. собирает `dist/dashboard` (linux/amd64) и упаковывает его в архив вместе со статикой `web/`
   и файлом окружения из секретов сервиса;
3. отправляет архив на сервер по SSH. Ключ деплоя на сервере привязан к [`dashboard-deploy`](dashboard-deploy)
   (`restrict,command=…`): скрипт ставит бинарник, статику и `/etc/dashboard/env` (root, 600),
   перезапускает сервис и проверяет `/healthz`. По этому ключу нельзя получить шелл или выполнить другую команду.

## Секреты окружения `production`

| Секрет | Что это |
|---|---|
| `DEPLOY_HOST` | адрес сервера |
| `DEPLOY_KNOWN_HOSTS` | строка `known_hosts` с ключом сервера |
| `DEPLOY_SSH_KEY` | приватный ключ деплоя (ed25519) |
| `TELEGRAM_BOT_TOKEN` | токен бота |
| `TELEGRAM_ALLOWED_USER_ID` | Telegram id владельца |
| `TRACKS_GITHUB_TOKEN` | fine-grained PAT на чтение репозиториев треков (Contents, Metadata); необязателен |

Имена на `GITHUB_` зарезервированы GitHub, поэтому токен треков — `TRACKS_GITHUB_TOKEN`; в сервис он попадает как `GITHUB_TOKEN`.

Окружение `production` доступно только ветке `main`: workflow из pull request секретов не получает.

## Сервер: один раз

- [`dashboard.service`](dashboard.service) → `/etc/systemd/system/`, `systemctl enable dashboard`.
- [`dashboard-deploy`](dashboard-deploy) → `/usr/local/sbin/` (root, 755).
- `/etc/dashboard/env` создаёт сам скрипт выкладки из секретов окружения.
- Публичная часть ключа деплоя в `authorized_keys`:
  `restrict,command="/usr/local/sbin/dashboard-deploy" ssh-ed25519 … github-actions-dashboard`.
- Обратный прокси перед `127.0.0.1:8090`: `/login`, `/auth/*`, `/api/*`, `/healthz` — открыто;
  всё остальное — только с сессией через `auth_request /auth/check`, без неё редирект на `/login`.

## Ручной запуск

Actions → Deploy → Run workflow.
