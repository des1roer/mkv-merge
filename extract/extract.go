package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var extractLogFile *os.File
var extractLogMu sync.Mutex

func setupExtractLog(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "out.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	extractLogFile = f
}

func elprint(s string) {
	fmt.Print(s)
	extractLogMu.Lock()
	if extractLogFile != nil {
		extractLogFile.WriteString(s)
	}
	extractLogMu.Unlock()
}

func elprintf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	elprint(s)
}

// Структуры для парсинга JSON от mkvmerge
type MkvTrackProperties struct {
	Language     string `json:"language"`
	LanguageIETF string `json:"language_ietf"`
	CodecID      string `json:"codec_id"`
}

type MkvTrack struct {
	ID         int                `json:"id"`
	Type       string             `json:"type"`
	Codec      string             `json:"codec"`
	Properties MkvTrackProperties `json:"properties"`
}

type MkvIdentifyOutput struct {
	Tracks []MkvTrack `json:"tracks"`
}

func main() {
	// Определяем директорию, где находится исполняемый файл
	execPath, err := os.Executable()
	if err != nil {
		elprintf("Ошибка: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Dir(execPath)
	setupExtractLog(baseDir)
	if extractLogFile != nil {
		defer extractLogFile.Close()
	}
	elprintf("Рабочая директория: %s\n", baseDir)

	// Проверяем наличие утилит из MKVToolNix
	if _, err := exec.LookPath("mkvmerge"); err != nil {
		elprint("Ошибка: mkvmerge не найден в PATH. Установите MKVToolNix.\n")
		os.Exit(1)
	}
	if _, err := exec.LookPath("mkvextract"); err != nil {
		elprint("Ошибка: mkvextract не найден в PATH. Установите MKVToolNix.\n")
		os.Exit(1)
	}

	// Создаём папку output
	outputDir := filepath.Join(baseDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		elprintf("Не удалось создать папку output: %v\n", err)
		os.Exit(1)
	}

	// Собираем список видеофайлов
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		elprintf("Ошибка чтения директории: %v\n", err)
		os.Exit(1)
	}

	var videoFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if isVideoFile(name) {
			videoFiles = append(videoFiles, name)
		}
	}

	if len(videoFiles) == 0 {
		elprint("Видеофайлов не найдено.\n")
		return
	}

	total := len(videoFiles)
	var completed int32
	var wg sync.WaitGroup

	// Ограничиваем параллельность до 2
	sem := make(chan struct{}, 2)

	elprintf("Найдено %d файлов. Обработка в 2 потока...\n\n", total)

	for _, fname := range videoFiles {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()
			sem <- struct{}{}        // занимаем слот
			defer func() { <-sem }() // освобождаем

			filePath := filepath.Join(baseDir, filename)
			elprintf("[%d/%d] Начало обработки: %s\n", atomic.LoadInt32(&completed)+1, total, filename)

			err := processVideo(filePath, outputDir)
			if err != nil {
				elprintf("  ❌ Ошибка в %s: %v\n", filename, err)
			} else {
				elprintf("  ✅ Завершено: %s\n", filename)
			}

			atomic.AddInt32(&completed, 1)
			percent := float64(atomic.LoadInt32(&completed)) / float64(total) * 100
			elprintf("📊 Прогресс: %d/%d (%.1f%%)\n\n", atomic.LoadInt32(&completed), total, percent)
		}(fname)
	}

	wg.Wait()
	elprint("✅ Все файлы обработаны.\n")
}

// isVideoFile проверяет расширение видеофайла
func isVideoFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	videoExts := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
		".webm": true, ".m4v": true, ".wmv": true, ".flv": true,
	}
	return videoExts[ext]
}

// parseEpisodeNumber извлекает номер эпизода из имени файла
func parseEpisodeNumber(filename string) int {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bep(?:isode)?[\s_.\-]*(\d+)`),
		regexp.MustCompile(`(?i)\bS\d+E(\d+)`),
		regexp.MustCompile(`(?i)\bсерия[\s_.\-]*(\d+)`),
		regexp.MustCompile(`(?i)\b[\s_.\-]*(\d+)$`),
	}
	for _, re := range patterns {
		match := re.FindStringSubmatch(base)
		if len(match) > 1 {
			num, _ := strconv.Atoi(match[1])
			if num > 0 {
				return num
			}
		}
	}
	return 0
}

// processVideo – извлечение русской аудиодорожки из одного файла
func processVideo(videoPath, outputDir string) error {
	// Получаем список всех дорожек
	tracks, err := getTracks(videoPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать дорожки: %w", err)
	}

	elprintf("  Найдено треков: %d\n", len(tracks))
	for i, t := range tracks {
		elprintf("    [%d] type=%s id=%d codec=%s lang=%s codec_id=%s\n",
			i, t.Type, t.ID, t.Codec, t.Properties.Language, t.Properties.CodecID)
	}

	// Ищем русскую аудиодорожку
	var rusTrack *MkvTrack
	for _, track := range tracks {
		if track.Type == "audio" {
			lang := track.Properties.Language
			if lang == "rus" || lang == "ru" {
				rusTrack = &track
				break
			}
		}
	}
	if rusTrack == nil {
		return fmt.Errorf("русская аудиодорожка не найдена")
	}

	// Определяем расширение выходного файла по кодекам
	ext := getExtensionForCodec(rusTrack.Codec, rusTrack.Properties.CodecID)
	if ext == "" {
		ext = ".mka" // на всякий случай
	}

	folderName := filepath.Base(filepath.Dir(videoPath))
	epNum := parseEpisodeNumber(filepath.Base(videoPath))
	outName := fmt.Sprintf("%s_%02d%s", folderName, epNum, ext)
	outPath := filepath.Join(outputDir, outName)

	// Извлекаем дорожку
	if err := extractTrack(videoPath, rusTrack.ID, outPath); err != nil {
		return fmt.Errorf("ошибка извлечения: %w", err)
	}

	elprintf("  🎵 Извлечена русская аудиодорожка (ID=%d) -> %s\n", rusTrack.ID, outName)
	return nil
}

// getTracks – запускает mkvmerge и парсит JSON
func getTracks(videoPath string) ([]MkvTrack, error) {
	cmd := exec.Command(
		"mkvmerge",
		"--identification-format", "json",
		"--identify",
		videoPath,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mkvmerge ошибка: %v, stderr: %s", err, stderr.String())
	}

	var data MkvIdentifyOutput
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		return nil, fmt.Errorf("не удалось разобрать JSON: %w\nraw: %s", err, stdout.String()[:min(200, stdout.Len())])
	}
	if len(data.Tracks) == 0 {
		return nil, fmt.Errorf("дорожки не найдены, raw: %s", stdout.String()[:min(200, stdout.Len())])
	}
	return data.Tracks, nil
}

// extractTrack – извлекает одну дорожку через mkvextract
func extractTrack(videoPath string, trackID int, outPath string) error {
	cmd := exec.Command(
		"mkvextract",
		"tracks",
		videoPath,
		fmt.Sprintf("%d:%s", trackID, outPath),
	)
	// Перенаправляем stderr, чтобы видеть возможные ошибки
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

// getExtensionForCodec – определяет расширение по кодекам
func getExtensionForCodec(codec, codecID string) string {
	switch {
	case strings.Contains(codecID, "A_AAC") || strings.Contains(codec, "AAC"):
		return ".aac"
	case strings.Contains(codecID, "A_EAC3") || strings.Contains(codec, "E-AC-3"):
		return ".eac3"
	case strings.Contains(codecID, "A_AC3") || strings.Contains(codec, "AC3"):
		return ".ac3"
	case strings.Contains(codecID, "A_DTS"):
		return ".dts"
	case strings.Contains(codecID, "A_FLAC"):
		return ".flac"
	case strings.Contains(codecID, "A_PCM"):
		return ".pcm"
	default:
		return ".mka"
	}
}
