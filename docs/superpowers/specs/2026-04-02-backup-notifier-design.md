# Дизайн: Backup Notifier Bot

**Дата:** 2026-04-02

## Цель

Расширить существующий эхо-бот до системы уведомлений о бэкапах Iperius Backup. Бот подключается к Яндекс IMAP, парсит входящие письма, сохраняет события в SQLite и рассылает уведомления подписчикам в мессенджере MAX. Поддерживает организации, управление пользователями и обнаружение "тишины" (пропущенных бэкапов).

---

## Архитектура

Один бинарник, три горутины:

```
main.go
├── goroutine: email.Worker  — IMAP polling → парсинг → chan BackupEvent
├── goroutine: bot.Bot       — MAX API polling + команды + рассылка
└── goroutine: bot.Watcher   — проверка "тишины" раз в час
         ↕
      db.DB (SQLite, shared)
```

**Структура файлов:**
```
max_notification/
├── main.go
├── internal/
│   ├── config/
│   │   └── config.go        # загрузка .env, структура Config
│   ├── db/
│   │   └── db.go            # SQLite: миграции, CRUD
│   ├── email/
│   │   └── worker.go        # IMAP polling, парсинг писем
│   └── bot/
│       ├── bot.go           # обработка команд, рассылка
│       └── watcher.go       # проверка пропущенных бэкапов
├── .env
├── .env.example
└── .gitignore
```

---

## Конфигурация (.env)

```
BOT_TOKEN=                    # токен MAX бота
ADMIN_USER_ID=                # user_id администратора в MAX
IMAP_HOST=imap.yandex.ru
IMAP_PORT=993
IMAP_USER=                    # email адрес
IMAP_PASSWORD=                # пароль приложения Яндекс
EMAIL_POLL_INTERVAL=5         # интервал проверки почты в минутах
DB_PATH=./data/bot.db         # путь к SQLite файлу
```

---

## База данных (SQLite)

### Таблица `users`
```sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,  -- user_id из MAX
    name        TEXT NOT NULL,        -- полное имя
    username    TEXT,                 -- @username
    status      TEXT NOT NULL DEFAULT 'pending', -- pending/active/rejected
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Таблица `organizations`
```sql
CREATE TABLE organizations (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE
);
```

### Таблица `user_organizations`
```sql
CREATE TABLE user_organizations (
    user_id INTEGER NOT NULL REFERENCES users(id),
    org_id  INTEGER NOT NULL REFERENCES organizations(id),
    PRIMARY KEY (user_id, org_id)
);
```

### Таблица `jobs`
```sql
CREATE TABLE jobs (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    job_name            TEXT NOT NULL UNIQUE, -- тема письма
    org_id              INTEGER REFERENCES organizations(id), -- nullable
    last_seen_at        DATETIME,
    avg_interval_hours  REAL,
    registered_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Таблица `backup_events`
```sql
CREATE TABLE backup_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES jobs(id),
    status      TEXT NOT NULL, -- success/failure/missed
    message     TEXT,          -- текст из тела письма или причина missed
    received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

---

## Email-воркер (`internal/email/worker.go`)

**Polling:** каждые `EMAIL_POLL_INTERVAL` минут подключается к `imap.yandex.ru:993` (TLS), ищет непрочитанные письма в INBOX.

**Парсинг:**
- `job_name` = тема письма (полностью, как есть)
- `status` определяется поиском ключевых слов в теле письма:
  - `success`: `successfully`, `успешно`, `завершено успешно`, `completed`
  - `failure`: `error`, `failed`, `ошибка`, `не удалось`, `завершено с ошибками`
  - Если ни одно не найдено → `failure` (безопасный дефолт)
- `message` = первые 500 символов тела письма

**После обработки:** письмо помечается как прочитанное (`\Seen`).

**Регистрация новой задачи:** если `job_name` не найден в `jobs` — создаём запись, `org_id = NULL`, уведомляем администратора: `⚙️ Новая задача зарегистрирована: "<job_name>". Привяжите её к организации: /setorg "<job_name>" <org_name>`

**Обновление статистики задачи:** при каждом новом письме:
- обновляем `last_seen_at = now`
- пересчитываем `avg_interval_hours` как среднее по последним 10 интервалам

---

## Watcher (`internal/bot/watcher.go`)

Запускается раз в час. Для каждой задачи у которой:
- `avg_interval_hours` не NULL (то есть было минимум 2 письма)
- `now - last_seen_at > 2 * avg_interval_hours`
- за последние `2 * avg_interval_hours` не было события типа `missed`

Создаёт `backup_event` со статусом `missed` и рассылает уведомление.

---

## Бот (`internal/bot/bot.go`)

### Проверка доступа
- Любой может писать `/start` и `/invite`
- Остальные команды — только пользователи со статусом `active`
- Команды `/pending`, `/approve`, `/reject`, `/users`, `/orgs`, `/addorg`, `/setorg` — только `ADMIN_USER_ID`

### Команды

**Публичные:**
- `/start` — приветствие: "Привет! Отправь /invite чтобы запросить доступ."
- `/invite` — если уже в БД: сообщить статус. Если нет: создать запись `pending`, уведомить администратора: `👤 Новый запрос доступа: <name> (@username). /approve <id> или /reject <id>`

**Для активных пользователей:**
- `/stats` — статистика за 7 и 30 дней по каждой задаче (только задачи из подписанных организаций):
  ```
  📊 Статистика за 7 дней:
  MyJob: ✅ 6  ❌ 1  ⚠️ 0

  За 30 дней:
  MyJob: ✅ 24  ❌ 2  ⚠️ 1
  ```
- `/last` — последние 10 событий (только из подписанных организаций):
  ```
  ✅ MyJob — 2026-04-02 14:30
  ❌ MyJob — 2026-04-02 02:00
  ```
- `/myorgs` — список организаций на которые подписан
- `/subscribe <org_name>` — подписаться
- `/unsubscribe <org_name>` — отписаться

**Для администратора:**
- `/pending` — список пользователей со статусом `pending`
- `/approve <user_id>` — установить статус `active`
- `/reject <user_id>` — установить статус `rejected`
- `/users` — список всех `active` пользователей с их организациями
- `/orgs` — список всех организаций и привязанных задач
- `/addorg <name>` — создать организацию
- `/setorg <job_name> <org_name>` — привязать задачу к организации

### Уведомления

Рассылаются автоматически при новом `BackupEvent`. Получатели:
- Администратор (`ADMIN_USER_ID`) — всегда
- Пользователи со статусом `active`, подписанные на организацию задачи (если `org_id != NULL`)
- Если `org_id = NULL` — только администратор

Формат:
```
✅ MyJob — успешно [2026-04-02 14:30]
❌ MyJob — ошибка: <message> [2026-04-02 14:30]
⚠️ MyJob — нет активности 26ч (ожидалось ~12ч) [2026-04-02 14:30]
```

---

## Поток данных

```
Яндекс IMAP
    → email.Worker (каждые N минут)
        → парсинг → BackupEvent
            → db.SaveEvent()
            → chan BackupEvent
                → bot.Bot → рассылка подписчикам

bot.Watcher (каждый час)
    → db.GetStalledJobs()
        → db.SaveEvent(missed)
            → chan BackupEvent
                → bot.Bot → рассылка
```

---

## Зависимости

- `github.com/max-messenger/max-bot-api-client-go` — MAX Bot SDK (уже установлен)
- `github.com/joho/godotenv` — загрузка .env (уже установлен)
- `github.com/emersion/go-imap/v2` — IMAP клиент
- `modernc.org/sqlite` — SQLite pure Go (без CGO, для статической сборки)
