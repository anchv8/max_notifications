package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner  = "anchv8"
	repoName   = "max_notifications"
	apiURL     = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
	binaryName = "max-notification.exe"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// CheckLatestVersion возвращает последний тег релиза с GitHub.
func CheckLatestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API вернул %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// Update скачивает новый бинарник и запускает bat-скрипт замены.
// isService=true — перезапуск через sc stop/start, иначе — прямой запуск exe.
func Update(ctx context.Context, currentVersion string, isService bool, serviceName string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("автообновление поддерживается только на Windows")
	}

	latest, err := CheckLatestVersion(ctx)
	if err != nil {
		return fmt.Errorf("проверка версии: %w", err)
	}

	// Нормализуем: убираем префикс "v" для сравнения
	latestClean := strings.TrimPrefix(latest, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")

	if latestClean == currentClean {
		return fmt.Errorf("уже установлена последняя версия (%s)", currentVersion)
	}

	downloadURL := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		repoOwner, repoName, latest, binaryName,
	)

	// Путь к текущему бинарнику
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("определение пути бинарника: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("абсолютный путь: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	newExePath := filepath.Join(exeDir, binaryName+".new")
	oldExePath := filepath.Join(exeDir, binaryName+".old")
	batPath := filepath.Join(exeDir, "update.bat")

	// Скачать новый бинарник
	if err := downloadFile(ctx, downloadURL, newExePath); err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}

	var bat string
	if isService {
		bat = fmt.Sprintf(`@echo off
ping 127.0.0.1 -n 3 > nul
sc stop %s > nul
ping 127.0.0.1 -n 3 > nul
del "%s" 2>nul
move "%s" "%s"
move "%s" "%s"
sc start %s > nul
del "%%~f0"
exit
`, serviceName, oldExePath, exePath, oldExePath, newExePath, exePath, serviceName)
	} else {
		bat = fmt.Sprintf(`@echo off
ping 127.0.0.1 -n 3 > nul
del "%s" 2>nul
move "%s" "%s"
move "%s" "%s"
start "" "%s"
del "%%~f0"
exit
`, oldExePath, exePath, oldExePath, newExePath, exePath, exePath)
	}

	if err := os.WriteFile(batPath, []byte(bat), 0755); err != nil {
		os.Remove(newExePath)
		return fmt.Errorf("создание скрипта обновления: %w", err)
	}

	// Запустить bat скрытно (без окна) и завершить текущий процесс
	cmd := exec.Command("cmd", "/C", "start", "/min", "", batPath)
	if err := cmd.Start(); err != nil {
		os.Remove(newExePath)
		os.Remove(batPath)
		return fmt.Errorf("запуск скрипта обновления: %w", err)
	}

	return nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер вернул %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
