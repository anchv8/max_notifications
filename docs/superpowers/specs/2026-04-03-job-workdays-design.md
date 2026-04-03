# Job Workdays Design

## Goal

Перенести расписание рабочих дней с уровня организации на уровень задания (job), как в PHP-логике: каждый job имеет своё расписание независимо от организации.

## Architecture

Три изменения: схема БД (новая таблица `job_workdays`), логика Watcher (проверка по job_id), команда `/workdays` (интерактивный выбор job'а кнопками).

---

## Секция 1: База данных

### Новая таблица

```sql
CREATE TABLE IF NOT EXISTS job_workdays (
    job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    day    INTEGER NOT NULL CHECK(day >= 1 AND day <= 7),
    PRIMARY KEY (job_id, day)
);
```

`ON DELETE CASCADE` — при удалении job'а расписание удаляется автоматически.

### Изменения в `internal/db/db.go`

**`migrate()`** — добавить создание `job_workdays`. Таблицу `org_workdays` оставить в migrate (не удалять код), удалить физически при деплое вручную через `sqlite3 bot.db "DROP TABLE IF EXISTS org_workdays;"`.

**Новые методы** (аналог существующих `GetOrgWorkdays` / `ToggleOrgWorkday` / `IsWorkday` — те остаются, но перестают использоваться):

```go
// GetJobWorkdays возвращает список рабочих дней для задания (1=Пн, 7=Вс).
func (d *DB) GetJobWorkdays(jobID int64) ([]int, error)

// ToggleJobWorkday переключает рабочий день для задания.
// Возвращает true если день добавлен, false если удалён.
func (d *DB) ToggleJobWorkday(jobID int64, day int) (bool, error)

// IsJobWorkday возвращает true если для задания сегодня рабочий день.
// Если рабочие дни не заданы — возвращает true (каждый день рабочий).
func (d *DB) IsJobWorkday(jobID int64) (bool, error)
```

`IsJobWorkday` — переименован относительно старого `IsWorkday` чтобы не путать. Логика идентична: нет записей → true, есть → проверить текущий день (1=Пн, 7=Вс через `time.Now().Weekday()`).

---

## Секция 2: Watcher

**Файл:** `internal/bot/watcher.go`

Одно изменение: заменить проверку org-уровня на job-уровень.

**Было:**
```go
if j.OrgID != nil {
    isWorkday, err := w.db.IsWorkday(*j.OrgID)
    ...
}
```

**Стало:**
```go
isWorkday, err := w.db.IsJobWorkday(j.ID)
if err != nil {
    log.Printf("[WATCHER] ошибка проверки рабочего дня для job=%d: %v", j.ID, err)
} else if !isWorkday {
    log.Printf("[WATCHER] пропускаем %q — сегодня нерабочий день", j.JobName)
    continue
}
```

Убирается условие `if j.OrgID != nil` — расписание работает для всех job'ов независимо от привязки к организации.

---

## Секция 3: Команда /workdays

**Файл:** `internal/bot/bot.go`

### Флоу

1. `/workdays` — бот показывает список всех job'ов кнопками. Рядом с каждым — аббревиатуры активных дней если заданы: `Backup Server 1 [Пн Вт Ср]`
2. Нажатие на job → бот редактирует сообщение: 7 кнопок дней (✅ день если активен) + `← Назад`
3. Нажатие на день → toggle, сообщение обновляется
4. `← Назад` → возврат к списку job'ов

### Callback data

| Callback | Действие |
|----------|----------|
| `wdjob:<jobID>` | Показать дни для job'а |
| `wdday:<jobID>:<day>` | Toggle дня (1-7) |
| `wdback` | Вернуться к списку job'ов |

### Изменения в bot.go

**`cmdWorkdays(ctx, chatID int64)`** — убрать аргумент `orgName`, вызывать `buildJobsKeyboard()` и отправлять сообщение "Выберите задание:".

**`buildJobsKeyboard() (string, *maxbot.Keyboard)`** — загружает все job'ы через `db.ListAllJobs()`, для каждого получает `GetJobWorkdays(job.ID)` и строит строку вида `JobName [Пн Вт]` или просто `JobName` если дней нет. По одной кнопке на строку.

**`buildDaysKeyboard(jobID int64) (string, *maxbot.Keyboard)`** — загружает job по ID, получает его workdays, строит 7 кнопок (✅ prefix если активен) + кнопку `← Назад`. Текст сообщения: `Дни для <b>JobName</b>:`.

**`handleCallback`** — добавить три ветки:
- `wdjob:<jobID>` → вызвать `buildDaysKeyboard`, отредактировать сообщение
- `wdday:<jobID>:<day>` → `ToggleJobWorkday`, затем `buildDaysKeyboard`, отредактировать
- `wdback` → `buildJobsKeyboard`, отредактировать сообщение

**`handleAdminCmd`** — `/workdays` теперь без аргументов: убрать проверку `len(parts) < 2` и `strings.Join(parts[1:], " ")`.

**`registerCommands`** — описание: `"Настроить рабочие дни задания"`.

**`cmdCommands`** — обновить описание `/workdays`.

### Аббревиатуры дней

```go
var dayNames = []string{"", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}
```

---

## Что НЕ меняется

- Таблица `org_workdays` и методы `GetOrgWorkdays`, `ToggleOrgWorkday`, `IsWorkday` — остаются в коде, просто не используются (удалить позже отдельным PR)
- Логика подписок, уведомлений, организаций
- Все остальные команды
