package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

// RunBackupScheduler запускает горутину автоматической отправки бэкапа БД.
func (b *Bot) RunBackupScheduler(ctx context.Context) {
	if b.cfg.DBBackupTime == "" {
		return
	}
	go func() {
		for {
			next := b.nextBackupTime()
			log.Printf("[BACKUP] следующий бэкап БД в %s", next.Format("2006-01-02 15:04"))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
				b.sendDBBackup(ctx, fmt.Sprintf("🗄 Автобэкап БД — %s", time.Now().In(b.cfg.Location).Format("2006-01-02 15:04")))
			}
		}
	}()
}

// nextBackupTime вычисляет следующий момент отправки бэкапа по расписанию.
func (b *Bot) nextBackupTime() time.Time {
	now := time.Now().In(b.cfg.Location)
	t, _ := time.ParseInLocation("15:04", b.cfg.DBBackupTime, b.cfg.Location)
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, b.cfg.Location)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// sendDBBackup загружает файл БД и отправляет всем админам.
func (b *Bot) sendDBBackup(ctx context.Context, caption string) {
	doc, err := b.api.Uploads.UploadMediaFromFile(ctx, schemes.FILE, b.cfg.DBPath)
	if err != nil {
		log.Printf("[BACKUP] ошибка загрузки файла БД: %v", err)
		for _, id := range b.cfg.AdminUserIDs {
			b.sendToUser(ctx, id, fmt.Sprintf("❌ Не удалось отправить бэкап БД: %v", err))
		}
		return
	}
	for _, id := range b.cfg.AdminUserIDs {
		msg := maxbot.NewMessage().SetUser(id).SetText(caption).AddFile(doc)
		if err := b.api.Messages.Send(ctx, msg); err != nil {
			log.Printf("[BACKUP] ошибка отправки бэкапа админу %d: %v", id, err)
		}
	}
}
