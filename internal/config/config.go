package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken          string
	AdminUserIDs      []int64
	IMAPHost          string
	IMAPPort          string
	IMAPUser          string
	IMAPPassword      string
	IMAPFolder        string
	EmailPollInterval int // минуты
	DBPath            string
	DBBackupTime      string    // "15:04", пусто — не отправлять автоматически
	Location          *time.Location
}

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Файл .env не найден, используются переменные окружения")
	}

	adminIDs, err := parseAdminIDs(os.Getenv("ADMIN_USER_IDS"))
	if err != nil {
		return nil, err
	}

	pollInterval, err := strconv.Atoi(os.Getenv("EMAIL_POLL_INTERVAL"))
	if err != nil || pollInterval <= 0 {
		pollInterval = 5
	}

	cfg := &Config{
		BotToken:          os.Getenv("BOT_TOKEN"),
		AdminUserIDs:      adminIDs,
		IMAPHost:          os.Getenv("IMAP_HOST"),
		IMAPPort:          os.Getenv("IMAP_PORT"),
		IMAPUser:          os.Getenv("IMAP_USER"),
		IMAPPassword:      os.Getenv("IMAP_PASSWORD"),
		IMAPFolder:        os.Getenv("IMAP_FOLDER"),
		EmailPollInterval: pollInterval,
		DBPath:            os.Getenv("DB_PATH"),
		DBBackupTime:      os.Getenv("DB_BACKUP_TIME"),
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN не задан")
	}
	if len(cfg.AdminUserIDs) == 0 {
		return nil, fmt.Errorf("ADMIN_USER_IDS не задан")
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
	if cfg.IMAPFolder == "" {
		cfg.IMAPFolder = "INBOX"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "./data/bot.db"
	}

	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		cfg.Location = time.Local
	} else {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("TIMEZONE %q невалидный: %w", tz, err)
		}
		cfg.Location = loc
	}

	if cfg.DBBackupTime != "" {
		if _, err := time.ParseInLocation("15:04", cfg.DBBackupTime, cfg.Location); err != nil {
			return nil, fmt.Errorf("DB_BACKUP_TIME %q невалидный, ожидается HH:MM: %w", cfg.DBBackupTime, err)
		}
	}

	return cfg, nil
}

func (c *Config) IsAdmin(userID int64) bool {
	for _, id := range c.AdminUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func parseAdminIDs(s string) ([]int64, error) {
	if s == "" {
		return nil, fmt.Errorf("ADMIN_USER_IDS не задан")
	}
	parts := strings.Split(s, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_USER_IDS: невалидный id %q: %w", p, err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("ADMIN_USER_IDS не задан")
	}
	return ids, nil
}
