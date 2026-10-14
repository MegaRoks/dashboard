.PHONY: test lint run initdata build-linux

# Локальный запуск читает server/.env (см. server/.env.example)
ENV = set -a; [ -f .env ] && . ./.env; set +a;

test:
	cd server && go test -race ./...

lint:
	cd server && test -z "$$(gofmt -l .)" && go vet ./...

run:
	cd server && $(ENV) go run ./cmd/dashboard

# Подписанный initData для входа без Telegram, пример — в README
initdata:
	@cd server && $(ENV) go run ./cmd/initdata

# Бинарник для сервера (Ubuntu, amd64). SQLite-драйвер на чистом Go, поэтому CGO не нужен
build-linux:
	cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../dist/dashboard ./cmd/dashboard
