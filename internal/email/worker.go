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
	Job      *db.Job
	Event    *db.BackupEvent
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
