package email_test

import (
	"testing"

	"max-echo-bot/internal/email"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		subject  string
		body     string
		expected string
	}{
		{"error in subject", "Backup error - MyOrg", "", "failure"},
		{"failure in subject", "Backup failure - MyOrg", "", "failure"},
		{"ошибка in subject", "Ошибка резервного копирования", "", "failure"},
		{"сбой in subject", "Сбой задания", "", "failure"},
		{"error in body", "Backup - MyOrg", "An error occurred", "failure"},
		{"failed in body", "Backup - MyOrg", "Task failed with code 5", "failure"},
		{"success no keywords", "Backup - MyOrg", "Backup finished", "success"},
		{"empty both", "", "", "success"},
		{"unknown text", "Some subject", "Some random text", "success"},
		{"case insensitive", "BACKUP ERROR - MyOrg", "", "failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := email.ParseStatus(tt.subject, tt.body)
			if got != tt.expected {
				t.Errorf("ParseStatus(%q, %q) = %q, want %q", tt.subject, tt.body, got, tt.expected)
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
