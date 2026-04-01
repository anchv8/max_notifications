package email

import "strings"

// ParseStatus определяет статус бэкапа по тексту письма.
// Возвращает "success" или "failure".
func ParseStatus(body string) string {
	lower := strings.ToLower(body)
	successKeywords := []string{
		"successfully", "успешно", "завершено успешно", "completed successfully",
		"backup completed", "резервное копирование завершено",
	}
	for _, kw := range successKeywords {
		if strings.Contains(lower, kw) {
			return "success"
		}
	}
	return "failure"
}

// TruncateMessage обрезает сообщение до maxLen символов.
func TruncateMessage(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
