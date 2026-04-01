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
	if cfg.IMAPPort == "" {
		return nil, fmt.Errorf("IMAP_PORT не задан")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "./data/bot.db"
	}

	return cfg, nil
}
