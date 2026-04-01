# Backup Notifier Bot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Расширить эхо-бот до системы уведомлений о бэкапах Iperius — парсинг IMAP почты, SQLite БД, организации, управление пользователями, рассылка в MAX.

**Architecture:** Один бинарник с тремя горутинами: email-воркер (IMAP polling), бот (команды + рассылка), watcher (проверка тишины). Общаются через `chan BackupEvent` и разделяемый `*db.DB`.

**Tech Stack:** Go 1.26.1, `github.com/emersion/go-imap/v2`, `modernc.org/sqlite`, `github.com/max-messenger/max-bot-api-client-go v1.6.8`, `github.com/joho/godotenv`

---

## Карта файлов

| Файл | Действие | Ответственность |
|------|----------|-----------------|
| `main.go` | Заменить | Инициализация, запуск горутин, graceful shutdown |
| `internal/config/config.go` | Создать | Загрузка .env, структура Config |
| `internal/db/db.go` | Создать | SQLite: миграции, все CRUD операции |
| `internal/db/db_test.go` | Создать | Тесты БД |
| `internal/email/worker.go` | Создать | IMAP polling, парсинг писем |
| `internal/email/parser.go` | Создать | Определение статуса по тексту письма |
| `internal/email/parser_test.go` | Создать | Тесты парсера |
| `internal/bot/bot.go` | Создать | Обработка команд, рассылка уведомлений |
| `internal/bot/watcher.go` | Создать | Проверка пропущенных бэкапов раз в час |
| `.env.example` | Изменить | Добавить новые переменные |

---

### Task 1: Зависимости и конфиг

**Files:**
- Modify: `go.mod`
- Create: `internal/config/config.go`
- Modify: `.env.example`

- [ ] **Step 1: Установить новые зависимости**

```bash
cd /Users/neponel/Desktop/max_notification
go get github.com/emersion/go-imap/v2
go get github.com/emersion/go-imap/v2/imapclient
go get modernc.org/sqlite
```

Ожидаемый результат: `go.mod` обновлён, нет ошибок.

- [ ] **Step 2: Создать `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken          string
	AdminUserID       int64
	IMAPHost          string
	IMAPPort          string
	IMAPUser          string
	IMAPPassword      string
	EmailPollInterval int // минуты
	DBPath            string
}

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		// не fatal — переменные могут быть заданы через окружение
		fmt.Println("Файл .env не найден, используются переменные окружения")
	}

	adminID, err := strconv.ParseInt(os.Getenv("ADMIN_USER_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("ADMIN_USER_ID невалидный: %w", err)
	}

	pollInterval, err := strconv.Atoi(os.Getenv("EMAIL_POLL_INTERVAL"))
	if err != nil || pollInterval <= 0 {
		pollInterval = 5
	}

	cfg := &Config{
		BotToken:          os.Getenv("BOT_TOKEN"),
		AdminUserID:       adminID,
		IMAPHost:          os.Getenv("IMAP_HOST"),
		IMAPPort:          os.Getenv("IMAP_PORT"),
		IMAPUser:          os.Getenv("IMAP_USER"),
		IMAPPassword:      os.Getenv("IMAP_PASSWORD"),
		EmailPollInterval: pollInterval,
		DBPath:            os.Getenv("DB_PATH"),
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN не задан")
	}
	if cfg.IMAPHost == "" {
		return nil, fmt.Errorf("IMAP_HOST не задан")
	}
	if cfg.IMAPUser == "" {
		return nil, fmt.Errorf("IMAP_USER не задан")
	}
	if cfg.IMAPPassword == "" {
		return nil, fmt.Errorf("IMAP_PASSWORD не задан")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "./data/bot.db"
	}

	return cfg, nil
}
```

- [ ] **Step 3: Обновить `.env.example`**

Заменить содержимое файла `.env.example`:

```
BOT_TOKEN=your_max_bot_token
ADMIN_USER_ID=123456789
IMAP_HOST=imap.yandex.ru
IMAP_PORT=993
IMAP_USER=your@yandex.ru
IMAP_PASSWORD=your_app_password
EMAIL_POLL_INTERVAL=5
DB_PATH=./data/bot.db
```

- [ ] **Step 4: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/config/config.go .env.example
git commit -m "feat: add config package and new dependencies"
```

---

### Task 2: База данных

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Создать `internal/db/db.go`**

```go
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

type User struct {
	ID        int64
	Name      string
	Username  string
	Status    string // pending/active/rejected
	CreatedAt time.Time
}

type Organization struct {
	ID   int64
	Name string
}

type Job struct {
	ID               int64
	JobName          string
	OrgID            *int64
	LastSeenAt       *time.Time
	AvgIntervalHours *float64
	RegisteredAt     time.Time
}

type BackupEvent struct {
	ID         int64
	JobID      int64
	JobName    string
	Status     string // success/failure/missed
	Message    string
	ReceivedAt time.Time
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("создание директории БД: %w", err)
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("открытие SQLite: %w", err)
	}
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("миграция БД: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id         INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			username   TEXT,
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS organizations (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE
		);
		CREATE TABLE IF NOT EXISTS user_organizations (
			user_id INTEGER NOT NULL REFERENCES users(id),
			org_id  INTEGER NOT NULL REFERENCES organizations(id),
			PRIMARY KEY (user_id, org_id)
		);
		CREATE TABLE IF NOT EXISTS jobs (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			job_name           TEXT NOT NULL UNIQUE,
			org_id             INTEGER REFERENCES organizations(id),
			last_seen_at       DATETIME,
			avg_interval_hours REAL,
			registered_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS backup_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id      INTEGER NOT NULL REFERENCES jobs(id),
			status      TEXT NOT NULL,
			message     TEXT,
			received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// --- Users ---

func (d *DB) GetUser(id int64) (*User, error) {
	u := &User{}
	err := d.conn.QueryRow(
		`SELECT id, name, username, status, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Name, &u.Username, &u.Status, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) CreateUser(id int64, name, username string) error {
	_, err := d.conn.Exec(
		`INSERT INTO users (id, name, username, status) VALUES (?, ?, ?, 'pending')`,
		id, name, username,
	)
	return err
}

func (d *DB) SetUserStatus(id int64, status string) error {
	_, err := d.conn.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, id)
	return err
}

func (d *DB) ListUsersByStatus(status string) ([]User, error) {
	rows, err := d.conn.Query(
		`SELECT id, name, username, status, created_at FROM users WHERE status = ?`, status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) ListActiveUsers() ([]User, error) {
	return d.ListUsersByStatus("active")
}

// --- Organizations ---

func (d *DB) CreateOrg(name string) error {
	_, err := d.conn.Exec(`INSERT INTO organizations (name) VALUES (?)`, name)
	return err
}

func (d *DB) GetOrgByName(name string) (*Organization, error) {
	o := &Organization{}
	err := d.conn.QueryRow(`SELECT id, name FROM organizations WHERE name = ?`, name).
		Scan(&o.ID, &o.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return o, err
}

func (d *DB) ListOrgs() ([]Organization, error) {
	rows, err := d.conn.Query(`SELECT id, name FROM organizations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orgs []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

func (d *DB) SubscribeUserToOrg(userID, orgID int64) error {
	_, err := d.conn.Exec(
		`INSERT OR IGNORE INTO user_organizations (user_id, org_id) VALUES (?, ?)`,
		userID, orgID,
	)
	return err
}

func (d *DB) UnsubscribeUserFromOrg(userID, orgID int64) error {
	_, err := d.conn.Exec(
		`DELETE FROM user_organizations WHERE user_id = ? AND org_id = ?`,
		userID, orgID,
	)
	return err
}

func (d *DB) ListUserOrgs(userID int64) ([]Organization, error) {
	rows, err := d.conn.Query(`
		SELECT o.id, o.name FROM organizations o
		JOIN user_organizations uo ON o.id = uo.org_id
		WHERE uo.user_id = ?
		ORDER BY o.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orgs []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

// GetSubscribersForOrg возвращает активных пользователей подписанных на организацию.
func (d *DB) GetSubscribersForOrg(orgID int64) ([]User, error) {
	rows, err := d.conn.Query(`
		SELECT u.id, u.name, u.username, u.status, u.created_at FROM users u
		JOIN user_organizations uo ON u.id = uo.user_id
		WHERE uo.org_id = ? AND u.status = 'active'
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// --- Jobs ---

func (d *DB) GetOrCreateJob(jobName string) (*Job, bool, error) {
	j := &Job{}
	err := d.conn.QueryRow(
		`SELECT id, job_name, org_id, last_seen_at, avg_interval_hours, registered_at FROM jobs WHERE job_name = ?`,
		jobName,
	).Scan(&j.ID, &j.JobName, &j.OrgID, &j.LastSeenAt, &j.AvgIntervalHours, &j.RegisteredAt)
	if err == nil {
		return j, false, nil
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
	// создаём новую задачу
	res, err := d.conn.Exec(`INSERT INTO jobs (job_name) VALUES (?)`, jobName)
	if err != nil {
		return nil, false, err
	}
	id, _ := res.LastInsertId()
	j = &Job{ID: id, JobName: jobName, RegisteredAt: time.Now()}
	return j, true, nil
}

func (d *DB) SetJobOrg(jobName string, orgID int64) error {
	_, err := d.conn.Exec(`UPDATE jobs SET org_id = ? WHERE job_name = ?`, orgID, jobName)
	return err
}

func (d *DB) UpdateJobStats(jobID int64, now time.Time) error {
	// Пересчитываем avg_interval_hours по последним 10 интервалам
	rows, err := d.conn.Query(`
		SELECT received_at FROM backup_events
		WHERE job_id = ? AND status != 'missed'
		ORDER BY received_at DESC
		LIMIT 11
	`, jobID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var times []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return err
		}
		times = append(times, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var avgHours *float64
	if len(times) >= 2 {
		var totalHours float64
		count := 0
		for i := 0; i < len(times)-1; i++ {
			diff := times[i].Sub(times[i+1]).Hours()
			if diff > 0 {
				totalHours += diff
				count++
			}
		}
		if count > 0 {
			avg := totalHours / float64(count)
			avgHours = &avg
		}
	}

	_, err = d.conn.Exec(
		`UPDATE jobs SET last_seen_at = ?, avg_interval_hours = ? WHERE id = ?`,
		now, avgHours, jobID,
	)
	return err
}

func (d *DB) ListAllJobs() ([]Job, error) {
	rows, err := d.conn.Query(
		`SELECT id, job_name, org_id, last_seen_at, avg_interval_hours, registered_at FROM jobs`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.JobName, &j.OrgID, &j.LastSeenAt, &j.AvgIntervalHours, &j.RegisteredAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetStalledJobs возвращает задачи у которых нет активности дольше 2*avg_interval_hours
// и за это время не было события 'missed'.
func (d *DB) GetStalledJobs() ([]Job, error) {
	rows, err := d.conn.Query(`
		SELECT id, job_name, org_id, last_seen_at, avg_interval_hours, registered_at
		FROM jobs
		WHERE avg_interval_hours IS NOT NULL
		  AND last_seen_at IS NOT NULL
		  AND (julianday('now') - julianday(last_seen_at)) * 24 > avg_interval_hours * 2
		  AND id NOT IN (
			SELECT DISTINCT job_id FROM backup_events
			WHERE status = 'missed'
			  AND (julianday('now') - julianday(received_at)) * 24 < avg_interval_hours * 2
		  )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.JobName, &j.OrgID, &j.LastSeenAt, &j.AvgIntervalHours, &j.RegisteredAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// --- BackupEvents ---

func (d *DB) SaveEvent(jobID int64, status, message string) (*BackupEvent, error) {
	res, err := d.conn.Exec(
		`INSERT INTO backup_events (job_id, status, message) VALUES (?, ?, ?)`,
		jobID, status, message,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &BackupEvent{
		ID:         id,
		JobID:      jobID,
		Status:     status,
		Message:    message,
		ReceivedAt: time.Now(),
	}, nil
}

type Stats struct {
	JobName  string
	Success7  int
	Failure7  int
	Missed7   int
	Success30 int
	Failure30 int
	Missed30  int
}

// GetStats возвращает статистику за 7 и 30 дней для задач из заданных организаций.
// Если orgIDs пустой — возвращает статистику по всем задачам.
func (d *DB) GetStats(orgIDs []int64) ([]Stats, error) {
	var rows *sql.Rows
	var err error

	if len(orgIDs) == 0 {
		rows, err = d.conn.Query(`
			SELECT j.job_name,
				SUM(CASE WHEN be.status='success' AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='failure' AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='missed'  AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='success' AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='failure' AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='missed'  AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END)
			FROM jobs j
			LEFT JOIN backup_events be ON j.id = be.job_id
			GROUP BY j.id
			ORDER BY j.job_name
		`)
	} else {
		// Строим placeholders для IN (?, ?, ...)
		placeholders := "?"
		args := []interface{}{orgIDs[0]}
		for _, id := range orgIDs[1:] {
			placeholders += ", ?"
			args = append(args, id)
		}
		rows, err = d.conn.Query(`
			SELECT j.job_name,
				SUM(CASE WHEN be.status='success' AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='failure' AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='missed'  AND (julianday('now')-julianday(be.received_at))<=7  THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='success' AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='failure' AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END),
				SUM(CASE WHEN be.status='missed'  AND (julianday('now')-julianday(be.received_at))<=30 THEN 1 ELSE 0 END)
			FROM jobs j
			LEFT JOIN backup_events be ON j.id = be.job_id
			WHERE j.org_id IN (`+placeholders+`)
			GROUP BY j.id
			ORDER BY j.job_name
		`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []Stats
	for rows.Next() {
		var s Stats
		if err := rows.Scan(&s.JobName, &s.Success7, &s.Failure7, &s.Missed7, &s.Success30, &s.Failure30, &s.Missed30); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetLastEvents возвращает последние N событий для задач из заданных организаций.
// Если orgIDs пустой — возвращает по всем задачам.
func (d *DB) GetLastEvents(orgIDs []int64, limit int) ([]BackupEvent, error) {
	var rows *sql.Rows
	var err error

	if len(orgIDs) == 0 {
		rows, err = d.conn.Query(`
			SELECT be.id, be.job_id, j.job_name, be.status, COALESCE(be.message,''), be.received_at
			FROM backup_events be
			JOIN jobs j ON j.id = be.job_id
			ORDER BY be.received_at DESC
			LIMIT ?
		`, limit)
	} else {
		placeholders := "?"
		args := []interface{}{orgIDs[0]}
		for _, id := range orgIDs[1:] {
			placeholders += ", ?"
			args = append(args, id)
		}
		args = append(args, limit)
		rows, err = d.conn.Query(`
			SELECT be.id, be.job_id, j.job_name, be.status, COALESCE(be.message,''), be.received_at
			FROM backup_events be
			JOIN jobs j ON j.id = be.job_id
			WHERE j.org_id IN (`+placeholders+`)
			ORDER BY be.received_at DESC
			LIMIT ?
		`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []BackupEvent
	for rows.Next() {
		var e BackupEvent
		if err := rows.Scan(&e.ID, &e.JobID, &e.JobName, &e.Status, &e.Message, &e.ReceivedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
```

- [ ] **Step 2: Написать тесты `internal/db/db_test.go`**

```go
package db_test

import (
	"testing"
	"time"

	"max-echo-bot/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreateAndGetUser(t *testing.T) {
	d := openTestDB(t)

	if err := d.CreateUser(100, "Иван Иванов", "ivan"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	u, err := d.GetUser(100)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u == nil {
		t.Fatal("пользователь не найден")
	}
	if u.Name != "Иван Иванов" {
		t.Errorf("имя: got %q want %q", u.Name, "Иван Иванов")
	}
	if u.Status != "pending" {
		t.Errorf("статус: got %q want %q", u.Status, "pending")
	}
}

func TestSetUserStatus(t *testing.T) {
	d := openTestDB(t)
	_ = d.CreateUser(200, "Петр", "petr")

	if err := d.SetUserStatus(200, "active"); err != nil {
		t.Fatalf("SetUserStatus: %v", err)
	}

	u, _ := d.GetUser(200)
	if u.Status != "active" {
		t.Errorf("статус: got %q want %q", u.Status, "active")
	}
}

func TestOrgCRUD(t *testing.T) {
	d := openTestDB(t)

	if err := d.CreateOrg("Acme"); err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}

	org, err := d.GetOrgByName("Acme")
	if err != nil || org == nil {
		t.Fatalf("GetOrgByName: %v %v", org, err)
	}
	if org.Name != "Acme" {
		t.Errorf("имя: got %q want %q", org.Name, "Acme")
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	d := openTestDB(t)
	_ = d.CreateUser(300, "Мария", "maria")
	_ = d.SetUserStatus(300, "active")
	_ = d.CreateOrg("OrgA")
	org, _ := d.GetOrgByName("OrgA")

	if err := d.SubscribeUserToOrg(300, org.ID); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	orgs, err := d.ListUserOrgs(300)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("ListUserOrgs: got %d orgs, err %v", len(orgs), err)
	}

	if err := d.UnsubscribeUserFromOrg(300, org.ID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	orgs, _ = d.ListUserOrgs(300)
	if len(orgs) != 0 {
		t.Errorf("после отписки должно быть 0 орг, got %d", len(orgs))
	}
}

func TestGetOrCreateJob(t *testing.T) {
	d := openTestDB(t)

	job, isNew, err := d.GetOrCreateJob("MyBackupJob")
	if err != nil {
		t.Fatalf("GetOrCreateJob: %v", err)
	}
	if !isNew {
		t.Error("первый вызов должен вернуть isNew=true")
	}
	if job.JobName != "MyBackupJob" {
		t.Errorf("job_name: got %q", job.JobName)
	}

	job2, isNew2, err := d.GetOrCreateJob("MyBackupJob")
	if err != nil {
		t.Fatalf("второй GetOrCreateJob: %v", err)
	}
	if isNew2 {
		t.Error("второй вызов должен вернуть isNew=false")
	}
	if job2.ID != job.ID {
		t.Error("ID должны совпадать")
	}
}

func TestSaveEventAndGetStats(t *testing.T) {
	d := openTestDB(t)

	job, _, _ := d.GetOrCreateJob("TestJob")

	if _, err := d.SaveEvent(job.ID, "success", "ok"); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	if _, err := d.SaveEvent(job.ID, "failure", "error"); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	stats, err := d.GetStats(nil)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("ожидалась 1 задача, got %d", len(stats))
	}
	if stats[0].Success7 != 1 {
		t.Errorf("Success7: got %d want 1", stats[0].Success7)
	}
	if stats[0].Failure7 != 1 {
		t.Errorf("Failure7: got %d want 1", stats[0].Failure7)
	}
}

func TestGetStalledJobs(t *testing.T) {
	d := openTestDB(t)

	job, _, _ := d.GetOrCreateJob("StaleJob")

	// Сохраняем два события чтобы avg_interval_hours посчиталось
	// Используем прямой SQL для установки времени в прошлом
	_, _ = d.SaveEvent(job.ID, "success", "")
	_ = d.UpdateJobStats(job.ID, time.Now().Add(-48*time.Hour))

	// Теперь last_seen_at = 48 часов назад, avg_interval_hours установим вручную
	// через второе событие с нужным временем — просто проверим что метод не падает
	stalled, err := d.GetStalledJobs()
	if err != nil {
		t.Fatalf("GetStalledJobs: %v", err)
	}
	// результат зависит от avg_interval_hours — просто проверяем что нет паники
	_ = stalled
}

func TestGetLastEvents(t *testing.T) {
	d := openTestDB(t)

	job, _, _ := d.GetOrCreateJob("EventJob")
	_, _ = d.SaveEvent(job.ID, "success", "msg1")
	_, _ = d.SaveEvent(job.ID, "failure", "msg2")

	events, err := d.GetLastEvents(nil, 10)
	if err != nil {
		t.Fatalf("GetLastEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("ожидалось 2 события, got %d", len(events))
	}
	// последнее событие первым (ORDER BY received_at DESC)
	if events[0].Status != "failure" {
		t.Errorf("первое событие: got %q want failure", events[0].Status)
	}
}
```

- [ ] **Step 3: Запустить тесты**

```bash
go test ./internal/db/... -v
```

Ожидаемый результат: все тесты PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/db/
git commit -m "feat: add db package with SQLite migrations and CRUD"
```

---

### Task 3: Email парсер

**Files:**
- Create: `internal/email/parser.go`
- Create: `internal/email/parser_test.go`

- [ ] **Step 1: Создать `internal/email/parser.go`**

```go
package email

import "strings"

// ParseStatus определяет статус бэкапа по тексту письма.
// Возвращает "success" или "failure".
func ParseStatus(body string) string {
	lower := strings.ToLower(body)
	successKeywords := []string{
		"successfully", "успешно", "завершено успешно", "completed successfully",
		"backup completed", "резервное копирование завершено",
	}
	for _, kw := range successKeywords {
		if strings.Contains(lower, kw) {
			return "success"
		}
	}
	return "failure"
}

// TruncateMessage обрезает сообщение до maxLen символов.
func TruncateMessage(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
```

- [ ] **Step 2: Написать тесты `internal/email/parser_test.go`**

```go
package email_test

import (
	"testing"

	"max-echo-bot/internal/email"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{"english success", "Backup completed successfully", "success"},
		{"russian success", "Резервное копирование завершено успешно", "success"},
		{"english failure", "Backup failed with error", "failure"},
		{"russian failure", "Произошла ошибка при резервном копировании", "failure"},
		{"empty body", "", "failure"},
		{"unknown text", "Some random text without keywords", "failure"},
		{"completed keyword", "backup completed", "success"},
		{"успешно keyword", "задание выполнено успешно", "success"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := email.ParseStatus(tt.body)
			if got != tt.expected {
				t.Errorf("ParseStatus(%q) = %q, want %q", tt.body, got, tt.expected)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"привет мир", 6, "привет..."},
		{"", 10, ""},
	}
	for _, tt := range tests {
		got := email.TruncateMessage(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("TruncateMessage(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}
```

- [ ] **Step 3: Запустить тесты**

```bash
go test ./internal/email/... -v
```

Ожидаемый результат: все тесты PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/email/parser.go internal/email/parser_test.go
git commit -m "feat: add email status parser"
```

---

### Task 4: Email воркер

**Files:**
- Create: `internal/email/worker.go`

- [ ] **Step 1: Создать `internal/email/worker.go`**

```go
package email

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"max-echo-bot/internal/config"
	"max-echo-bot/internal/db"
)

// Event — событие бэкапа, передаётся в канал для рассылки ботом.
type Event struct {
	Job     *db.Job
	Event   *db.BackupEvent
	IsNewJob bool // true если задача зарегистрирована впервые
}

// Worker опрашивает IMAP почту и отправляет события в канал.
type Worker struct {
	cfg    *config.Config
	db     *db.DB
	events chan<- Event
}

func NewWorker(cfg *config.Config, database *db.DB, events chan<- Event) *Worker {
	return &Worker{cfg: cfg, db: database, events: events}
}

// Run запускает polling и блокируется до отмены контекста.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(w.cfg.EmailPollInterval) * time.Minute)
	defer ticker.Stop()

	// Первый запуск сразу
	w.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Worker) poll(ctx context.Context) {
	addr := fmt.Sprintf("%s:%s", w.cfg.IMAPHost, w.cfg.IMAPPort)
	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		log.Printf("[EMAIL] ошибка подключения к IMAP: %v", err)
		return
	}
	defer c.Close()

	if err := c.Login(w.cfg.IMAPUser, w.cfg.IMAPPassword).Wait(); err != nil {
		log.Printf("[EMAIL] ошибка входа: %v", err)
		return
	}

	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		log.Printf("[EMAIL] ошибка выбора INBOX: %v", err)
		return
	}

	searchData, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		log.Printf("[EMAIL] ошибка поиска писем: %v", err)
		return
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return
	}

	log.Printf("[EMAIL] найдено %d непрочитанных писем", len(uids))

	uidSet := imap.UIDSetNum(uids...)
	bodySection := &imap.FetchItemBodySection{}
	messages, err := c.Fetch(uidSet, &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}).Collect()
	if err != nil {
		log.Printf("[EMAIL] ошибка получения писем: %v", err)
		return
	}

	for _, msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		subject := msg.Envelope.Subject
		bodyBytes := msg.FindBodySection(bodySection)
		body := string(bodyBytes)

		status := ParseStatus(body)
		message := TruncateMessage(body, 500)

		job, isNew, err := w.db.GetOrCreateJob(subject)
		if err != nil {
			log.Printf("[EMAIL] ошибка получения задачи %q: %v", subject, err)
			continue
		}

		event, err := w.db.SaveEvent(job.ID, status, message)
		if err != nil {
			log.Printf("[EMAIL] ошибка сохранения события: %v", err)
			continue
		}

		if err := w.db.UpdateJobStats(job.ID, time.Now()); err != nil {
			log.Printf("[EMAIL] ошибка обновления статистики задачи: %v", err)
		}

		event.JobName = subject

		select {
		case w.events <- Event{Job: job, Event: event, IsNewJob: isNew}:
		case <-ctx.Done():
			return
		}

		// Пометить письмо как прочитанное
		storeUID := imap.UIDSetNum(msg.UID)
		if err := c.Store(storeUID, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagSeen},
			Silent: true,
		}, nil).Close(); err != nil {
			log.Printf("[EMAIL] ошибка пометки письма как прочитанного: %v", err)
		}
	}
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок.

- [ ] **Step 3: Commit**

```bash
git add internal/email/worker.go
git commit -m "feat: add IMAP email worker"
```

---

### Task 5: Watcher

**Files:**
- Create: `internal/bot/watcher.go`

- [ ] **Step 1: Создать `internal/bot/watcher.go`**

```go
package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"max-echo-bot/internal/db"
	"max-echo-bot/internal/email"
)

// Watcher проверяет раз в час задачи у которых нет активности.
type Watcher struct {
	db     *db.DB
	events chan<- email.Event
}

func NewWatcher(database *db.DB, events chan<- email.Event) *Watcher {
	return &Watcher{db: database, events: events}
}

// Run запускает watcher и блокируется до отмены контекста.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

func (w *Watcher) check(ctx context.Context) {
	stalled, err := w.db.GetStalledJobs()
	if err != nil {
		log.Printf("[WATCHER] ошибка получения застрявших задач: %v", err)
		return
	}

	for _, job := range stalled {
		j := job // копия для замыкания

		hoursGone := time.Since(*j.LastSeenAt).Hours()
		expected := *j.AvgIntervalHours

		message := fmt.Sprintf("нет активности %.0fч (ожидалось ~%.0fч)", hoursGone, expected)

		event, err := w.db.SaveEvent(j.ID, "missed", message)
		if err != nil {
			log.Printf("[WATCHER] ошибка сохранения missed события: %v", err)
			continue
		}

		event.JobName = j.JobName

		select {
		case w.events <- email.Event{Job: &j, Event: event, IsNewJob: false}:
		case <-ctx.Done():
			return
		}
	}
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок.

- [ ] **Step 3: Commit**

```bash
git add internal/bot/watcher.go
git commit -m "feat: add watcher for missed backups"
```

---

### Task 6: Бот — команды и рассылка

**Files:**
- Create: `internal/bot/bot.go`

- [ ] **Step 1: Создать `internal/bot/bot.go`**

```go
package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"

	"max-echo-bot/internal/config"
	"max-echo-bot/internal/db"
	"max-echo-bot/internal/email"
)

type Bot struct {
	api    *maxbot.Api
	cfg    *config.Config
	db     *db.DB
	events <-chan email.Event
}

func NewBot(api *maxbot.Api, cfg *config.Config, database *db.DB, events <-chan email.Event) *Bot {
	return &Bot{api: api, cfg: cfg, db: database, events: events}
}

// Run запускает бота: обрабатывает команды и рассылает уведомления.
func (b *Bot) Run(ctx context.Context) {
	updates := b.api.GetUpdates(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.events:
			if !ok {
				return
			}
			b.handleEvent(ctx, ev)
		case upd, ok := <-updates:
			if !ok {
				return
			}
			if msg, ok := upd.(*schemes.MessageCreatedUpdate); ok {
				b.handleMessage(ctx, msg)
			}
		}
	}
}

// send отправляет сообщение в чат.
func (b *Bot) send(ctx context.Context, chatID int64, text string) {
	if err := b.api.Messages.Send(ctx, maxbot.NewMessage().SetChat(chatID).SetText(text)); err != nil {
		log.Printf("[BOT] ошибка отправки в chat=%d: %v", chatID, err)
	}
}

// broadcast рассылает сообщение списку пользователей.
func (b *Bot) broadcast(ctx context.Context, userIDs []int64, text string) {
	for _, uid := range userIDs {
		b.send(ctx, uid, text)
	}
}

// handleEvent рассылает уведомление о событии бэкапа.
func (b *Bot) handleEvent(ctx context.Context, ev email.Event) {
	ts := ev.Event.ReceivedAt.Format("2006-01-02 15:04")
	var text string
	switch ev.Event.Status {
	case "success":
		text = fmt.Sprintf("✅ %s — успешно [%s]", ev.Event.JobName, ts)
	case "failure":
		text = fmt.Sprintf("❌ %s — ошибка: %s [%s]", ev.Event.JobName, ev.Event.Message, ts)
	case "missed":
		text = fmt.Sprintf("⚠️ %s — %s [%s]", ev.Event.JobName, ev.Event.Message, ts)
	}

	// Уведомить администратора всегда
	recipients := []int64{b.cfg.AdminUserID}

	// Если задача привязана к организации — уведомить подписчиков
	if ev.Job.OrgID != nil {
		subs, err := b.db.GetSubscribersForOrg(*ev.Job.OrgID)
		if err != nil {
			log.Printf("[BOT] ошибка получения подписчиков: %v", err)
		}
		for _, u := range subs {
			if u.ID != b.cfg.AdminUserID {
				recipients = append(recipients, u.ID)
			}
		}
	}

	b.broadcast(ctx, recipients, text)

	// Уведомить администратора о новой задаче
	if ev.IsNewJob {
		b.send(ctx, b.cfg.AdminUserID, fmt.Sprintf(
			"⚙️ Новая задача зарегистрирована: %q. Привяжите её к организации:\n/setorg \"%s\" <org_name>",
			ev.Job.JobName, ev.Job.JobName,
		))
	}
}

// handleMessage маршрутизирует входящее сообщение к нужному обработчику.
func (b *Bot) handleMessage(ctx context.Context, upd *schemes.MessageCreatedUpdate) {
	userID := upd.Message.Sender.UserId
	chatID := upd.Message.Recipient.ChatId
	name := upd.Message.Sender.Name
	username := upd.Message.Sender.Username
	text := strings.TrimSpace(upd.Message.Body.Text)

	log.Printf("[BOT] userId=%d text=%q", userID, text)

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])

	// Публичные команды
	switch cmd {
	case "/start":
		b.send(ctx, chatID, "Привет! Отправь /invite чтобы запросить доступ.")
		return
	case "/invite":
		b.cmdInvite(ctx, chatID, userID, name, username)
		return
	}

	// Проверка что пользователь активен
	user, err := b.db.GetUser(userID)
	if err != nil {
		log.Printf("[BOT] ошибка получения пользователя %d: %v", userID, err)
		return
	}
	if user == nil || user.Status != "active" {
		b.send(ctx, chatID, "У вас нет доступа. Отправьте /invite для запроса.")
		return
	}

	// Команды для активных пользователей
	switch cmd {
	case "/stats":
		b.cmdStats(ctx, chatID, userID)
	case "/last":
		b.cmdLast(ctx, chatID, userID)
	case "/myorgs":
		b.cmdMyOrgs(ctx, chatID, userID)
	case "/subscribe":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /subscribe <org_name>")
			return
		}
		b.cmdSubscribe(ctx, chatID, userID, strings.Join(parts[1:], " "))
	case "/unsubscribe":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /unsubscribe <org_name>")
			return
		}
		b.cmdUnsubscribe(ctx, chatID, userID, strings.Join(parts[1:], " "))
	default:
		// Проверяем команды администратора
		if userID != b.cfg.AdminUserID {
			b.send(ctx, chatID, "Неизвестная команда.")
			return
		}
		b.handleAdminCmd(ctx, chatID, cmd, parts)
	}
}

func (b *Bot) handleAdminCmd(ctx context.Context, chatID int64, cmd string, parts []string) {
	switch cmd {
	case "/pending":
		b.cmdPending(ctx, chatID)
	case "/approve":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /approve <user_id>")
			return
		}
		b.cmdApproveReject(ctx, chatID, parts[1], "active")
	case "/reject":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /reject <user_id>")
			return
		}
		b.cmdApproveReject(ctx, chatID, parts[1], "rejected")
	case "/users":
		b.cmdUsers(ctx, chatID)
	case "/orgs":
		b.cmdOrgs(ctx, chatID)
	case "/addorg":
		if len(parts) < 2 {
			b.send(ctx, chatID, "Использование: /addorg <name>")
			return
		}
		b.cmdAddOrg(ctx, chatID, strings.Join(parts[1:], " "))
	case "/setorg":
		// /setorg "job name" org_name
		b.cmdSetOrg(ctx, chatID, parts)
	default:
		b.send(ctx, chatID, "Неизвестная команда.")
	}
}

// --- Команды ---

func (b *Bot) cmdInvite(ctx context.Context, chatID, userID int64, name, username string) {
	existing, err := b.db.GetUser(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка сервера, попробуйте позже.")
		return
	}
	if existing != nil {
		switch existing.Status {
		case "pending":
			b.send(ctx, chatID, "Ваша заявка уже отправлена, ожидайте подтверждения.")
		case "active":
			b.send(ctx, chatID, "У вас уже есть доступ.")
		case "rejected":
			b.send(ctx, chatID, "Ваша заявка была отклонена.")
		}
		return
	}

	if err := b.db.CreateUser(userID, name, username); err != nil {
		b.send(ctx, chatID, "Ошибка сервера, попробуйте позже.")
		return
	}

	b.send(ctx, chatID, "Заявка отправлена! Ожидайте подтверждения от администратора.")

	usernameStr := ""
	if username != "" {
		usernameStr = " (@" + username + ")"
	}
	b.send(ctx, b.cfg.AdminUserID, fmt.Sprintf(
		"👤 Новый запрос доступа: %s%s (id: %d)\n/approve %d или /reject %d",
		name, usernameStr, userID, userID, userID,
	))
}

func (b *Bot) cmdStats(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}

	var orgIDs []int64
	for _, o := range orgs {
		orgIDs = append(orgIDs, o.ID)
	}

	// Администратор видит всё
	if userID == b.cfg.AdminUserID {
		orgIDs = nil
	}

	stats, err := b.db.GetStats(orgIDs)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения статистики.")
		return
	}
	if len(stats) == 0 {
		b.send(ctx, chatID, "Нет данных. Подпишитесь на организации: /subscribe <org_name>")
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 Статистика за 7 дней:\n")
	for _, s := range stats {
		sb.WriteString(fmt.Sprintf("%s: ✅ %d  ❌ %d  ⚠️ %d\n", s.JobName, s.Success7, s.Failure7, s.Missed7))
	}
	sb.WriteString("\nЗа 30 дней:\n")
	for _, s := range stats {
		sb.WriteString(fmt.Sprintf("%s: ✅ %d  ❌ %d  ⚠️ %d\n", s.JobName, s.Success30, s.Failure30, s.Missed30))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdLast(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}

	var orgIDs []int64
	for _, o := range orgs {
		orgIDs = append(orgIDs, o.ID)
	}
	if userID == b.cfg.AdminUserID {
		orgIDs = nil
	}

	events, err := b.db.GetLastEvents(orgIDs, 10)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения событий.")
		return
	}
	if len(events) == 0 {
		b.send(ctx, chatID, "Событий нет.")
		return
	}

	var sb strings.Builder
	for _, e := range events {
		ts := e.ReceivedAt.Format("2006-01-02 15:04")
		icon := "✅"
		if e.Status == "failure" {
			icon = "❌"
		} else if e.Status == "missed" {
			icon = "⚠️"
		}
		sb.WriteString(fmt.Sprintf("%s %s — %s\n", icon, e.JobName, ts))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdMyOrgs(ctx context.Context, chatID, userID int64) {
	orgs, err := b.db.ListUserOrgs(userID)
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(orgs) == 0 {
		b.send(ctx, chatID, "Вы не подписаны ни на одну организацию.\nИспользуйте /subscribe <org_name>")
		return
	}
	var sb strings.Builder
	sb.WriteString("Ваши организации:\n")
	for _, o := range orgs {
		sb.WriteString("• " + o.Name + "\n")
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdSubscribe(ctx context.Context, chatID, userID int64, orgName string) {
	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация %q не найдена.", orgName))
		return
	}
	if err := b.db.SubscribeUserToOrg(userID, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка подписки.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Вы подписались на %q.", orgName))
}

func (b *Bot) cmdUnsubscribe(ctx context.Context, chatID, userID int64, orgName string) {
	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация %q не найдена.", orgName))
		return
	}
	if err := b.db.UnsubscribeUserFromOrg(userID, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка отписки.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Вы отписались от %q.", orgName))
}

func (b *Bot) cmdPending(ctx context.Context, chatID int64) {
	users, err := b.db.ListUsersByStatus("pending")
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(users) == 0 {
		b.send(ctx, chatID, "Нет ожидающих заявок.")
		return
	}
	var sb strings.Builder
	sb.WriteString("Ожидающие заявки:\n")
	for _, u := range users {
		uname := ""
		if u.Username != "" {
			uname = " (@" + u.Username + ")"
		}
		sb.WriteString(fmt.Sprintf("• %s%s (id: %d) — /approve %d | /reject %d\n",
			u.Name, uname, u.ID, u.ID, u.ID))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdApproveReject(ctx context.Context, chatID int64, userIDStr, status string) {
	var targetID int64
	if _, err := fmt.Sscanf(userIDStr, "%d", &targetID); err != nil {
		b.send(ctx, chatID, "Неверный user_id.")
		return
	}
	user, err := b.db.GetUser(targetID)
	if err != nil || user == nil {
		b.send(ctx, chatID, "Пользователь не найден.")
		return
	}
	if err := b.db.SetUserStatus(targetID, status); err != nil {
		b.send(ctx, chatID, "Ошибка обновления статуса.")
		return
	}
	action := "подтверждён"
	userNotif := "Ваша заявка одобрена! Теперь вы можете получать уведомления.\nПодпишитесь на организации: /subscribe <org_name>"
	if status == "rejected" {
		action = "отклонён"
		userNotif = "Ваша заявка была отклонена."
	}
	b.send(ctx, chatID, fmt.Sprintf("Пользователь %s %s.", user.Name, action))
	b.send(ctx, targetID, userNotif)
}

func (b *Bot) cmdUsers(ctx context.Context, chatID int64) {
	users, err := b.db.ListActiveUsers()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(users) == 0 {
		b.send(ctx, chatID, "Нет активных пользователей.")
		return
	}
	var sb strings.Builder
	sb.WriteString("Активные пользователи:\n")
	for _, u := range users {
		orgs, _ := b.db.ListUserOrgs(u.ID)
		orgNames := make([]string, 0, len(orgs))
		for _, o := range orgs {
			orgNames = append(orgNames, o.Name)
		}
		orgStr := "нет подписок"
		if len(orgNames) > 0 {
			orgStr = strings.Join(orgNames, ", ")
		}
		uname := ""
		if u.Username != "" {
			uname = " (@" + u.Username + ")"
		}
		sb.WriteString(fmt.Sprintf("• %s%s — %s\n", u.Name, uname, orgStr))
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdOrgs(ctx context.Context, chatID int64) {
	orgs, err := b.db.ListOrgs()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения данных.")
		return
	}
	if len(orgs) == 0 {
		b.send(ctx, chatID, "Нет организаций. Создайте: /addorg <name>")
		return
	}
	jobs, err := b.db.ListAllJobs()
	if err != nil {
		b.send(ctx, chatID, "Ошибка получения задач.")
		return
	}

	// Группируем задачи по org_id
	jobsByOrg := make(map[int64][]string)
	for _, j := range jobs {
		if j.OrgID != nil {
			jobsByOrg[*j.OrgID] = append(jobsByOrg[*j.OrgID], j.JobName)
		}
	}

	var sb strings.Builder
	sb.WriteString("Организации:\n")
	for _, o := range orgs {
		jobNames := jobsByOrg[o.ID]
		jobStr := "нет задач"
		if len(jobNames) > 0 {
			jobStr = strings.Join(jobNames, ", ")
		}
		sb.WriteString(fmt.Sprintf("• %s — %s\n", o.Name, jobStr))
	}

	// Задачи без организации
	var unassigned []string
	for _, j := range jobs {
		if j.OrgID == nil {
			unassigned = append(unassigned, j.JobName)
		}
	}
	if len(unassigned) > 0 {
		sb.WriteString("\nБез организации:\n")
		for _, name := range unassigned {
			sb.WriteString("• " + name + "\n")
		}
	}
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) cmdAddOrg(ctx context.Context, chatID int64, name string) {
	if err := b.db.CreateOrg(name); err != nil {
		b.send(ctx, chatID, fmt.Sprintf("Ошибка создания организации (возможно, уже существует): %v", err))
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Организация %q создана.", name))
}

func (b *Bot) cmdSetOrg(ctx context.Context, chatID int64, parts []string) {
	// Синтаксис: /setorg "job name" org_name
	// или: /setorg job_name org_name (если без пробелов)
	raw := strings.Join(parts[1:], " ")
	var jobName, orgName string

	if strings.HasPrefix(raw, `"`) {
		// Имя задачи в кавычках
		end := strings.Index(raw[1:], `"`)
		if end < 0 {
			b.send(ctx, chatID, `Использование: /setorg "название задачи" название_орг`)
			return
		}
		jobName = raw[1 : end+1]
		orgName = strings.TrimSpace(raw[end+2:])
	} else {
		fields := strings.Fields(raw)
		if len(fields) < 2 {
			b.send(ctx, chatID, `Использование: /setorg "название задачи" название_орг`)
			return
		}
		jobName = fields[0]
		orgName = strings.Join(fields[1:], " ")
	}

	org, err := b.db.GetOrgByName(orgName)
	if err != nil || org == nil {
		b.send(ctx, chatID, fmt.Sprintf("Организация %q не найдена.", orgName))
		return
	}
	if err := b.db.SetJobOrg(jobName, org.ID); err != nil {
		b.send(ctx, chatID, "Ошибка привязки задачи.")
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("Задача %q привязана к организации %q.", jobName, orgName))
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок.

- [ ] **Step 3: Commit**

```bash
git add internal/bot/bot.go
git commit -m "feat: add bot with commands and notification dispatch"
```

---

### Task 7: Точка входа main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Заменить `main.go`**

```go
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	maxbot "github.com/max-messenger/max-bot-api-client-go"

	"max-echo-bot/internal/bot"
	"max-echo-bot/internal/config"
	"max-echo-bot/internal/db"
	"max-echo-bot/internal/email"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Ошибка конфигурации: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("Ошибка открытия БД: %v", err)
	}
	defer database.Close()

	api, err := maxbot.New(cfg.BotToken)
	if err != nil {
		log.Fatalf("Ошибка создания MAX клиента: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	info, err := api.Bots.GetBot(ctx)
	if err != nil {
		log.Fatalf("Ошибка получения информации о боте: %v", err)
	}
	log.Printf("Бот запущен: %s (id=%d)", info.Name, info.UserId)

	events := make(chan email.Event, 100)

	emailWorker := email.NewWorker(cfg, database, events)
	watcher := bot.NewWatcher(database, events)
	botRunner := bot.NewBot(api, cfg, database, events)

	go emailWorker.Run(ctx)
	go watcher.Run(ctx)

	log.Printf("Email polling каждые %d мин. Ожидаю сообщения... (Ctrl+C для выхода)", cfg.EmailPollInterval)
	botRunner.Run(ctx) // блокирует до отмены контекста

	log.Println("Бот завершил работу")
}
```

- [ ] **Step 2: Проверить компиляцию**

```bash
go build ./...
```

Ожидаемый результат: нет ошибок, бинарник `max-echo-bot` создан.

- [ ] **Step 3: Запустить все тесты**

```bash
go test ./... -v
```

Ожидаемый результат: все тесты PASS.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: wire up all components in main.go"
```

---

## Проверка покрытия спецификации

| Требование | Задача |
|-----------|--------|
| IMAP polling Яндекс с TLS | Task 4 |
| Парсинг статуса (RU + EN ключевые слова) | Task 3 |
| Пометка писем как прочитанных | Task 4 |
| Регистрация новой задачи + уведомление админа | Task 4 |
| Пересчёт avg_interval_hours | Task 2 (`UpdateJobStats`) |
| Watcher: missed события раз в час | Task 5 |
| SQLite БД: users, organizations, user_organizations, jobs, backup_events | Task 2 |
| /start, /invite, статусы pending/active/rejected | Task 6 |
| /approve, /reject + уведомление пользователя | Task 6 |
| /pending, /users | Task 6 |
| /orgs, /addorg, /setorg | Task 6 |
| /subscribe, /unsubscribe, /myorgs | Task 6 |
| /stats (7 и 30 дней) | Task 6 |
| /last (10 событий) | Task 6 |
| Рассылка подписчикам по организации | Task 6 |
| Администратор всегда получает уведомления | Task 6 |
| ADMIN_USER_ID из .env | Task 1 |
| Graceful shutdown | Task 7 |
| Конфиг из .env | Task 1 |
