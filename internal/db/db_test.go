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

	// Verify method returns no error and correct type on empty DB
	stalled, err := d.GetStalledJobs()
	if err != nil {
		t.Fatalf("GetStalledJobs должен не падать на пустой БД: %v", err)
	}
	if stalled == nil {
		stalled = []db.Job{}
	}
	if len(stalled) != 0 {
		t.Errorf("пустая БД не должна возвращать stalled задачи, got %d", len(stalled))
	}

	// Задача без avg_interval_hours (только одно событие) не должна быть stalled
	job, _, _ := d.GetOrCreateJob("OnceSeenJob")
	_, _ = d.SaveEvent(job.ID, "success", "")
	_ = d.UpdateJobStats(job.ID, time.Now().Add(-72*time.Hour))

	stalled, err = d.GetStalledJobs()
	if err != nil {
		t.Fatalf("GetStalledJobs: %v", err)
	}
	if len(stalled) != 0 {
		t.Errorf("задача с одним событием (avg=nil) не должна быть stalled, got %d", len(stalled))
	}
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
	if events[0].Status != "failure" {
		t.Errorf("первое событие: got %q want failure", events[0].Status)
	}
}
