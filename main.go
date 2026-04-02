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

// Version задаётся при сборке: go build -ldflags "-X main.Version=1.0.0"
var Version = "dev"

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
	botRunner := bot.NewBot(api, cfg, database, events, emailWorker, Version)

	go emailWorker.Run(ctx)
	go watcher.Run(ctx)

	log.Printf("Email polling каждые %d мин. Ожидаю сообщения... (Ctrl+C для выхода)", cfg.EmailPollInterval)
	botRunner.Run(ctx)

	log.Println("Бот завершил работу")
}
