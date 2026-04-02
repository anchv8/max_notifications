# MAX Echo Bot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Простой эхо-бот для мессенджера MAX на Go — отвечает на текстовые сообщения, логирует все события с userId, корректно завершается по сигналу.

**Architecture:** Один файл `main.go` с polling через официальный SDK. Токен загружается из `.env` через `godotenv`. Graceful shutdown через `signal.NotifyContext`.

**Tech Stack:** Go 1.21+, `github.com/max-messenger/max-bot-api-client-go`, `github.com/joho/godotenv`

---

## Карта файлов

| Файл | Действие | Ответственность |
|------|----------|-----------------|
| `go.mod` | Создать | Модуль и зависимости |
| `go.sum` | Создаётся автоматически | Хэши зависимостей |
| `.env.example` | Создать | Шаблон конфига |
| `.gitignore` | Создать | Исключить `.env` и бинарники |
| `main.go` | Создать | Вся логика бота |
| `.env` | Создать вручную | Токен (не коммитить) |

---

### Task 1: Инициализация Go-модуля

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Инициализировать модуль**

```bash
cd /path/to/max_notification
go mod init max-echo-bot
```

Ожидаемый результат: создан файл `go.mod` с содержимым:
```
module max-echo-bot

go 1.21
```

- [ ] **Step 2: Установить зависимости**

```bash
go get github.com/max-messenger/max-bot-api-client-go
go get github.com/joho/godotenv
```

Ожидаемый результат: обновлён `go.mod`, создан `go.sum`.

- [ ] **Step 3: Проверить go.mod**

```bash
cat go.mod
```

Ожидаемый результат — файл содержит обе зависимости:
```
require (
    github.com/joho/godotenv v1.x.x
    github.com/max-messenger/max-bot-api-client-go v1.x.x
)
```

- [ ] **Step 4: Commit**

```bash
git init
git add go.mod go.sum
git commit -m "chore: init go module with dependencies"
```

---

### Task 2: Конфигурационные файлы

**Files:**
- Create: `.env.example`
- Create: `.gitignore`

- [ ] **Step 1: Создать `.env.example`**

Создать файл `.env.example` с содержимым:
```
BOT_TOKEN=your_token_here
```

- [ ] **Step 2: Создать `.gitignore`**

Создать файл `.gitignore` с содержимым:
```
.env
max-echo-bot
*.exe
```

- [ ] **Step 3: Commit**

```bash
git add .env.example .gitignore
git commit -m "chore: add config template and gitignore"
```

---

### Task 3: Реализация main.go

**Files:**
- Create: `main.go`

- [ ] **Step 1: Создать `main.go`**

Создать файл `main.go` со следующим содержимым:

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Файл .env не найден, используются переменные окружения")
	}

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN не задан")
	}

	api := maxbot.New(token)

	info, err := api.Bots.GetBot()
	if err != nil {
		log.Fatalf("Ошибка получения информации о боте: %v", err)
	}
	log.Printf("Бот запущен: %s (id=%d)", info.Name, info.UserId)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("Ожидаю сообщения... (Ctrl+C для выхода)")

	for upd := range api.GetUpdates(ctx) {
		switch u := upd.(type) {
		case *schemes.MessageCreatedUpdate:
			userID := u.Message.Sender.UserId
			text := u.Message.Body.Text
			chatID := u.Message.Recipient.ChatId
			log.Printf("[MSG] userId=%d text=%q", userID, text)

			_, err := api.Messages.Send(
				maxbot.NewMessage().SetChat(chatID).SetText(text),
			)
			if err != nil {
				log.Printf("[ERR] не удалось отправить эхо: %v", err)
			}
		default:
			log.Printf("[UPD] тип=%T", upd)
		}
	}

	log.Println("Бот завершил работу")
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок, создан бинарник `max-echo-bot` (или `max-echo-bot.exe` на Windows).

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: implement MAX echo bot with graceful shutdown"
```

---

### Task 4: Создать .env и запустить бота

**Files:**
- Create: `.env` (вручную, не коммитить)

- [ ] **Step 1: Создать .env с токеном**

```bash
cp .env.example .env
```

Открыть `.env` и вставить реальный токен:
```
BOT_TOKEN=ваш_токен_здесь
```

Токен получается через MasterBot в мессенджере MAX: открыть диалог с MasterBot, создать нового бота и скопировать токен.

- [ ] **Step 2: Запустить бота**

```bash
go run main.go
```

Ожидаемый результат в консоли:
```
2026/04/01 12:00:00 Бот запущен: MyBot (id=123456789)
2026/04/01 12:00:00 Ожидаю сообщения... (Ctrl+C для выхода)
```

- [ ] **Step 3: Проверить работу**

Открыть мессенджер MAX, найти своего бота и отправить любое сообщение, например `Привет`.

Ожидаемый результат в консоли:
```
2026/04/01 12:00:10 [MSG] userId=987654321 text="Привет"
```

Ожидаемый результат в мессенджере: бот отвечает `Привет`.

- [ ] **Step 4: Проверить graceful shutdown**

Нажать Ctrl+C.

Ожидаемый результат:
```
2026/04/01 12:00:20 Бот завершил работу
```

Процесс завершается без паники или зависания.

- [ ] **Step 5: Проверить логирование других событий**

Если бот добавлен в группу или запущен заново — в консоли должна появиться строка вида:
```
2026/04/01 12:00:05 [UPD] тип=*schemes.BotStartedUpdate
```

---

## Проверка покрытия спецификации

| Требование из спека | Задача |
|---------------------|--------|
| Отвечать на текстовые сообщения эхом | Task 3, Step 2 |
| Логировать userId и текст | Task 3, Step 2 — `log.Printf("[MSG] userId=%d text=%q", ...)` |
| Логировать все остальные события | Task 3, Step 2 — `default: log.Printf("[UPD] тип=%T", ...)` |
| Graceful shutdown по SIGINT/SIGTERM | Task 3, Step 2 — `signal.NotifyContext` |
| Токен из `.env` | Task 1 + Task 2 + Task 4 |
| Ошибка если токен пустой | Task 3, Step 2 — `log.Fatal("BOT_TOKEN не задан")` |
