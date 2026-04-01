package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	maxbot "github.com/max-messenger/max-bot-api-client-go"
	"github.com/max-messenger/max-bot-api-client-go/schemes"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Файл .env не найден, используются переменные окружения")
	}

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN не задан")
	}

	api, err := maxbot.New(token)
	if err != nil {
		log.Fatalf("Ошибка создания клиента: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	info, err := api.Bots.GetBot(ctx)
	if err != nil {
		log.Fatalf("Ошибка получения информации о боте: %v", err)
	}
	log.Printf("Бот запущен: %s (id=%d)", info.Name, info.UserId)

	log.Println("Ожидаю сообщения... (Ctrl+C для выхода)")

	for upd := range api.GetUpdates(ctx) {
		switch u := upd.(type) {
		case *schemes.MessageCreatedUpdate:
			userID := u.Message.Sender.UserId
			text := u.Message.Body.Text
			chatID := u.Message.Recipient.ChatId
			log.Printf("[MSG] userId=%d text=%q", userID, text)

			if text == "" {
				break
			}

			err := api.Messages.Send(
				ctx,
				maxbot.NewMessage().SetChat(chatID).SetText(text),
			)
			if err != nil {
				log.Printf("[ERR] не удалось отправить эхо: %v", err)
			}
		default:
			log.Printf("[UPD] тип=%T", upd)
		}
	}

	log.Println("Бот завершил работу")
}
