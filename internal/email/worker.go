package email

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"strings"

	"golang.org/x/text/encoding/charmap"
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
			w.poll(ctx) //nolint:errcheck
		}
	}
}

// Poll запускает одну проверку почты вручную, возвращает кол-во обработанных писем и ошибку.
func (w *Worker) Poll(ctx context.Context) (int, error) {
	return w.poll(ctx)
}

// TestConnection проверяет подключение к IMAP без обработки писем.
func (w *Worker) TestConnection() error {
	addr := fmt.Sprintf("%s:%s", w.cfg.IMAPHost, w.cfg.IMAPPort)
	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return fmt.Errorf("подключение: %w", err)
	}
	defer c.Close()

	if err := c.Login(w.cfg.IMAPUser, w.cfg.IMAPPassword).Wait(); err != nil {
		return fmt.Errorf("авторизация: %w", err)
	}
	return nil
}

func (w *Worker) poll(ctx context.Context) (int, error) {
	addr := fmt.Sprintf("%s:%s", w.cfg.IMAPHost, w.cfg.IMAPPort)
	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		log.Printf("[EMAIL] ошибка подключения к IMAP: %v", err)
		return 0, fmt.Errorf("подключение: %w", err)
	}
	defer c.Close()

	if err := c.Login(w.cfg.IMAPUser, w.cfg.IMAPPassword).Wait(); err != nil {
		log.Printf("[EMAIL] ошибка входа: %v", err)
		return 0, fmt.Errorf("авторизация: %w", err)
	}

	if _, err := c.Select(w.cfg.IMAPFolder, nil).Wait(); err != nil {
		log.Printf("[EMAIL] ошибка выбора папки %s: %v", w.cfg.IMAPFolder, err)
		return 0, fmt.Errorf("выбор папки: %w", err)
	}

	searchData, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		log.Printf("[EMAIL] ошибка поиска писем: %v", err)
		return 0, fmt.Errorf("поиск писем: %w", err)
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return 0, nil
	}

	log.Printf("[EMAIL] найдено %d непрочитанных писем", len(uids))

	uidSet := imap.UIDSetNum(uids...)
	bodySection := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierText}
	messages, err := c.Fetch(uidSet, &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}).Collect()
	if err != nil {
		log.Printf("[EMAIL] ошибка получения писем: %v", err)
		return 0, fmt.Errorf("получение писем: %w", err)
	}

	processed := 0
	for _, msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		subject := decodeSubject(msg.Envelope.Subject)
		bodyBytes := msg.FindBodySection(bodySection)
		body := decodeBody(bodyBytes, msg)

		// Пометить письмо как прочитанное ДО сохранения в БД,
		// чтобы избежать дублирования при краше после сохранения.
		storeUID := imap.UIDSetNum(msg.UID)
		if err := c.Store(storeUID, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagSeen},
			Silent: true,
		}, nil).Close(); err != nil {
			log.Printf("[EMAIL] ошибка пометки письма как прочитанного: %v", err)
		}

		status := ParseStatus(subject, body)
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
		processed++

		select {
		case w.events <- Event{Job: job, Event: event, IsNewJob: isNew}:
		case <-ctx.Done():
			return processed, nil
		}
	}
	return processed, nil
}

func decodeBody(raw []byte, _ *imapclient.FetchMessageBuffer) string {
	// Try quoted-printable first (most common for HTML emails)
	qpReader := quotedprintable.NewReader(strings.NewReader(string(raw)))
	decoded, err := io.ReadAll(qpReader)
	if err == nil && len(decoded) > 0 {
		return string(decoded)
	}
	// Try base64
	b64decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err == nil && len(b64decoded) > 0 {
		return string(b64decoded)
	}
	// Return as-is
	return string(raw)
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "koi8-r":
		return charmap.KOI8R.NewDecoder().Reader(input), nil
	case "koi8-u":
		return charmap.KOI8U.NewDecoder().Reader(input), nil
	case "windows-1251", "cp1251":
		return charmap.Windows1251.NewDecoder().Reader(input), nil
	case "iso-8859-1", "latin-1":
		return charmap.ISO8859_1.NewDecoder().Reader(input), nil
	default:
		return input, nil
	}
}

func decodeSubject(s string) string {
	dec := &mime.WordDecoder{
		CharsetReader: charsetReader,
	}
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}
