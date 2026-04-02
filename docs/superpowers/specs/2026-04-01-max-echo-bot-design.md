# Дизайн: Echo-бот для мессенджера MAX

**Дата:** 2026-04-01

## Цель

Простой эхо-бот для мессенджера MAX на Go, который:
- Отвечает на текстовые сообщения, повторяя их обратно
- Логирует все входящие события в консоль (включая userId и текст для текстовых сообщений)
- Корректно завершается при получении сигнала SIGINT/SIGTERM

## Структура проекта

```
max_notification/
├── .env              # BOT_TOKEN=<токен> (не коммитить)
├── .env.example      # шаблон с пустым BOT_TOKEN
├── .gitignore        # исключает .env
├── go.mod
├── go.sum
└── main.go
```

## Зависимости

- `github.com/max-messenger/max-bot-api-client-go` — официальный SDK MAX
- `github.com/joho/godotenv` — загрузка `.env` файла

## Архитектура `main.go`

### 1. Загрузка конфига
`godotenv.Load()` читает `.env` при старте. Токен берётся из `os.Getenv("BOT_TOKEN")`. Если токен пустой — программа завершается с ошибкой.

### 2. Инициализация клиента
```go
api := maxbot.New(token)
```

### 3. Graceful shutdown
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```
При Ctrl+C контекст отменяется, цикл обновлений завершается чисто.

### 4. Цикл обновлений
```go
for upd := range api.GetUpdates(ctx) { ... }
```
`GetUpdates` возвращает канал; когда контекст отменён — канал закрывается автоматически.

### 5. Обработка обновлений

```
switch upd.(type):
  *schemes.MessageCreatedUpdate → log(userId, text) + отправить эхо
  любой другой тип              → log(тип события)
```

**Логирование для текстовых сообщений:**
```
[MSG] userId=12345 text="привет"
```

**Логирование для остальных событий:**
```
[UPD] тип=*schemes.BotStartedUpdate
```

**Эхо-ответ:**
```go
api.Messages.Send(
    maxbot.NewMessage().
        SetChat(upd.Message.Recipient.ChatId).
        SetText(upd.Message.Body.Text),
)
```

## Поток данных

```
MAX API → GetUpdates(ctx) → канал обновлений → switch по типу
                                                   ├── MessageCreated → log(userId, text) + echo reply
                                                   └── другое         → log(тип)
```

## Конфигурация

`.env`:
```
BOT_TOKEN=your_token_here
```

Токен получается через MasterBot в мессенджере MAX.

## Запуск

```bash
cp .env.example .env
# вставить токен в .env
go run main.go
```
