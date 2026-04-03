package email

import "strings"

// subjectErrorPrefixes — паттерны начала темы, однозначно указывающие на ошибку (из PHP-логики).
var subjectErrorPrefixes = []string{
	"Backup ended with errors",
	"Backup interrupted",
	"ERRORS!",
	"Ошибка!",
	"Завершено с ошибками",
}

// subjectSuccessPrefixes — паттерны начала темы, однозначно указывающие на успех.
var subjectSuccessPrefixes = []string{
	"Выполнена задача",
}

// errorKeywords — ключевые слова в теме+теле, указывающие на ошибку (уровень 2).
var errorKeywords = []string{
	"error", "failure", "failed",
	"ошибка", "сбой", "не удалось", "завершено с ошибками",
}

// ParseStatus определяет статус бэкапа по теме и телу письма.
// Возвращает "success" или "failure".
// Уровень 1: проверка префиксов темы (точное начало строки, без toLower).
// Уровень 2: поиск ключевых слов в теме+теле (toLower).
func ParseStatus(subject, body string) string {
	// Уровень 1 — успех по префиксу (быстрый выход)
	for _, p := range subjectSuccessPrefixes {
		if strings.HasPrefix(subject, p) {
			return "success"
		}
	}
	// Уровень 1 — ошибка по префиксу
	for _, p := range subjectErrorPrefixes {
		if strings.HasPrefix(subject, p) {
			return "failure"
		}
	}
	// Уровень 2 — ключевые слова в теме+теле
	combined := strings.ToLower(subject + " " + body)
	for _, kw := range errorKeywords {
		if strings.Contains(combined, kw) {
			return "failure"
		}
	}
	return "success"
}

// ExtractOrgName извлекает название организации из темы письма по PHP-логике:
// берёт всё после первого символа "-" и триммирует пробелы.
// Пример: "1cbases - Ленком 1с" → "Ленком 1с"
// Если дефис не найден — возвращает "".
func ExtractOrgName(subject string) string {
	idx := strings.Index(subject, "-")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(subject[idx+1:])
}

// TruncateMessage обрезает сообщение до maxLen символов.
func TruncateMessage(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
