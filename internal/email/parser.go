package email

import "strings"

// errorKeywords — ключевые слова, указывающие на ошибку бэкапа.
// Проверяются в теме и теле письма; если ни одно не найдено — успех.
var errorKeywords = []string{
	"error", "failure", "failed",
	"ошибка", "сбой", "не удалось", "завершено с ошибками",
}

// ParseStatus определяет статус бэкапа по теме и телу письма.
// Возвращает "success" или "failure".
func ParseStatus(subject, body string) string {
	combined := strings.ToLower(subject + " " + body)
	for _, kw := range errorKeywords {
		if strings.Contains(combined, kw) {
			return "failure"
		}
	}
	return "success"
}

// TruncateMessage обрезает сообщение до maxLen символов.
func TruncateMessage(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
