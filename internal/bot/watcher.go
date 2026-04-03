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

		// Пропускаем если для задания сегодня нерабочий день
		isWorkday, err := w.db.IsJobWorkday(j.ID)
		if err != nil {
			log.Printf("[WATCHER] ошибка проверки рабочего дня для job=%d: %v", j.ID, err)
		} else if !isWorkday {
			log.Printf("[WATCHER] пропускаем %q — сегодня нерабочий день", j.JobName)
			continue
		}

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
