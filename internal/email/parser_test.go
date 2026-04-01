package email_test

import (
	"testing"

	"max-echo-bot/internal/email"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{"english success", "Backup completed successfully", "success"},
		{"russian success", "Резервное копирование завершено успешно", "success"},
		{"english failure", "Backup failed with error", "failure"},
		{"russian failure", "Произошла ошибка при резервном копировании", "failure"},
		{"empty body", "", "failure"},
		{"unknown text", "Some random text without keywords", "failure"},
		{"completed keyword", "backup completed", "success"},
		{"успешно keyword", "задание выполнено успешно", "success"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := email.ParseStatus(tt.body)
			if got != tt.expected {
				t.Errorf("ParseStatus(%q) = %q, want %q", tt.body, got, tt.expected)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"привет мир", 6, "привет..."},
		{"", 10, ""},
	}
	for _, tt := range tests {
		got := email.TruncateMessage(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("TruncateMessage(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}
