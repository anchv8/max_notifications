package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// RunDailyReporter запускает горутину автоматической отправки ежедневного отчёта.
func (b *Bot) RunDailyReporter(ctx context.Context) {
	if b.cfg.ReportTime == "" {
		return
	}
	go func() {
		for {
			next := b.nextReportTime()
			log.Printf("[REPORT] следующий отчёт в %s", next.Format("2006-01-02 15:04"))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
				text := b.buildDailyReport()
				for _, id := range b.cfg.AdminUserIDs {
					b.sendToUser(ctx, id, text)
				}
			}
		}
	}()
}

// nextReportTime вычисляет следующий момент отправки отчёта по расписанию.
func (b *Bot) nextReportTime() time.Time {
	now := time.Now().In(b.cfg.Location)
	t, err := time.ParseInLocation("15:04", b.cfg.ReportTime, b.cfg.Location)
	if err != nil {
		log.Printf("[REPORT] невалидный ReportTime %q: %v", b.cfg.ReportTime, err)
		return now.Add(24 * time.Hour)
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, b.cfg.Location)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// buildDailyReport формирует текст ежедневного отчёта:
// успешные задачи одной строкой, проблемные (failure+missed) — списком.
func (b *Bot) buildDailyReport() string {
	now := time.Now().In(b.cfg.Location)
	dateStr := now.Format("02.01.2006")

	successes, err := b.db.GetRecentSuccesses(24)
	if err != nil {
		log.Printf("[REPORT] ошибка получения успехов: %v", err)
	}
	problems, err := b.db.GetRecentErrors(24)
	if err != nil {
		log.Printf("[REPORT] ошибка получения ошибок: %v", err)
	}

	if len(problems) == 0 {
		return fmt.Sprintf("📋 <b>Отчёт за %s:</b> всё в порядке ✅", dateStr)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>Отчёт за %s:</b>\n", dateStr))

	if len(successes) > 0 {
		// Дедупликация по job_name
		seen := make(map[string]bool)
		var names []string
		for _, e := range successes {
			if !seen[e.JobName] {
				seen[e.JobName] = true
				names = append(names, e.JobName)
			}
		}
		sb.WriteString(fmt.Sprintf("\n✅ <b>Ок:</b> %s\n", strings.Join(names, ", ")))
	}

	sb.WriteString(fmt.Sprintf("\n❌ <b>Проблемы (%d):</b>\n", len(problems)))
	for _, e := range problems {
		ts := e.ReceivedAt.In(b.cfg.Location).Format("02.01 15:04")
		switch e.Status {
		case "failure":
			sb.WriteString(fmt.Sprintf("• <b>%s</b> — ошибка (<code>%s</code>)\n", e.JobName, ts))
		case "missed":
			sb.WriteString(fmt.Sprintf("• <b>%s</b> — нет активности (<code>%s</code>)\n", e.JobName, ts))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}
