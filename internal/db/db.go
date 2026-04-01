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
	var createdAt string
	err := d.conn.QueryRow(
		`SELECT id, name, username, status, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Name, &u.Username, &u.Status, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = parseTime(createdAt)
	return u, nil
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
		var createdAt string
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &u.Status, &createdAt); err != nil {
			return nil, err
		}
		u.CreatedAt = parseTime(createdAt)
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
		var createdAt string
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &u.Status, &createdAt); err != nil {
			return nil, err
		}
		u.CreatedAt = parseTime(createdAt)
		users = append(users, u)
	}
	return users, rows.Err()
}

// --- Jobs ---

func (d *DB) GetOrCreateJob(jobName string) (*Job, bool, error) {
	j := &Job{}
	var lastSeenAt sql.NullString
	var avgIntervalHours sql.NullFloat64
	var registeredAt string
	err := d.conn.QueryRow(
		`SELECT id, job_name, org_id, last_seen_at, avg_interval_hours, registered_at FROM jobs WHERE job_name = ?`,
		jobName,
	).Scan(&j.ID, &j.JobName, &j.OrgID, &lastSeenAt, &avgIntervalHours, &registeredAt)
	if err == nil {
		j.RegisteredAt = parseTime(registeredAt)
		if lastSeenAt.Valid {
			t := parseTime(lastSeenAt.String)
			j.LastSeenAt = &t
		}
		if avgIntervalHours.Valid {
			j.AvgIntervalHours = &avgIntervalHours.Float64
		}
		return j, false, nil
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}
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
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return err
		}
		times = append(times, parseTime(ts))
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
		var lastSeenAt sql.NullString
		var avgIntervalHours sql.NullFloat64
		var registeredAt string
		if err := rows.Scan(&j.ID, &j.JobName, &j.OrgID, &lastSeenAt, &avgIntervalHours, &registeredAt); err != nil {
			return nil, err
		}
		j.RegisteredAt = parseTime(registeredAt)
		if lastSeenAt.Valid {
			t := parseTime(lastSeenAt.String)
			j.LastSeenAt = &t
		}
		if avgIntervalHours.Valid {
			j.AvgIntervalHours = &avgIntervalHours.Float64
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

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
		var lastSeenAt sql.NullString
		var avgIntervalHours sql.NullFloat64
		var registeredAt string
		if err := rows.Scan(&j.ID, &j.JobName, &j.OrgID, &lastSeenAt, &avgIntervalHours, &registeredAt); err != nil {
			return nil, err
		}
		j.RegisteredAt = parseTime(registeredAt)
		if lastSeenAt.Valid {
			t := parseTime(lastSeenAt.String)
			j.LastSeenAt = &t
		}
		if avgIntervalHours.Valid {
			j.AvgIntervalHours = &avgIntervalHours.Float64
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
	JobName   string
	Success7  int
	Failure7  int
	Missed7   int
	Success30 int
	Failure30 int
	Missed30  int
}

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

func (d *DB) GetLastEvents(orgIDs []int64, limit int) ([]BackupEvent, error) {
	var rows *sql.Rows
	var err error

	if len(orgIDs) == 0 {
		rows, err = d.conn.Query(`
			SELECT be.id, be.job_id, j.job_name, be.status, COALESCE(be.message,''), be.received_at
			FROM backup_events be
			JOIN jobs j ON j.id = be.job_id
			ORDER BY be.received_at DESC, be.id DESC
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
			ORDER BY be.received_at DESC, be.id DESC
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
		var receivedAt string
		if err := rows.Scan(&e.ID, &e.JobID, &e.JobName, &e.Status, &e.Message, &receivedAt); err != nil {
			return nil, err
		}
		e.ReceivedAt = parseTime(receivedAt)
		events = append(events, e)
	}
	return events, rows.Err()
}

// parseTime parses SQLite DATETIME strings into time.Time.
// SQLite stores DATETIME as "2006-01-02 15:04:05" (UTC).
func parseTime(s string) time.Time {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
