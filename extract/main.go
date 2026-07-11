package main

import (
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

// Структуры для парсинга JSON от mkvmerge
type MkvTrack struct {
	ID         int    `json:"id"`
	Type       string `json:"type"`
	Codec      string `json:"codec"`
	Properties struct {
		Language     string `json:"language"`
		LanguageIETF string `json:"language_ietf"`
		CodecID      string `json:"codec_id"`
	} `json:"properties"`
}

type MkvIdentifyOutput struct {
	Tracks []MkvTrack `json:"tracks"`
}

func main() {
	// Определяем директорию, где находится исполняемый файл
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Ошибка: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Dir(execPath)
	fmt.Printf("Рабочая директория: %s\n", baseDir)

	// Проверяем наличие утилит из MKVToolNix
	if _, err := exec.LookPath("mkvmerge"); err != nil {
		fmt.Println("Ошибка: mkvmerge не найден в PATH. Установите MKVToolNix.")
		os.Exit(1)
	}
	if _, err := exec.LookPath("mkvextract"); err != nil {
		fmt.Println("Ошибка: mkvextract не найден в PATH. Установите MKVToolNix.")
		os.Exit(1)
	}

	// Создаём папку output
	outputDir := filepath.Join(baseDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Не удалось создать папку output: %v\n", err)
		os.Exit(1)
	}

	// Собираем список видеофайлов
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		fmt.Printf("Ошибка чтения директории: %v\n", err)
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
		fmt.Println("Видеофайлов не найдено.")
		return
	}

	total := len(videoFiles)
	var completed int32
	var wg sync.WaitGroup

	// Ограничиваем параллельность до 2
	sem := make(chan struct{}, 2)

	fmt.Printf("Найдено %d файлов. Обработка в 2 потока...\n\n", total)

	for _, fname := range videoFiles {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()
			sem <- struct{}{}        // занимаем слот
			defer func() { <-sem }() // освобождаем

			filePath := filepath.Join(baseDir, filename)
			fmt.Printf("[%d/%d] Начало обработки: %s\n", atomic.LoadInt32(&completed)+1, total, filename)

			err := processVideo(filePath, outputDir)
			if err != nil {
				fmt.Printf("  ❌ Ошибка в %s: %v\n", filename, err)
			} else {
				fmt.Printf("  ✅ Завершено: %s\n", filename)
			}

			atomic.AddInt32(&completed, 1)
			percent := float64(atomic.LoadInt32(&completed)) / float64(total) * 100
			fmt.Printf("📊 Прогресс: %d/%d (%.1f%%)\n\n", atomic.LoadInt32(&completed), total, percent)
		}(fname)
	}

	wg.Wait()
	fmt.Println("✅ Все файлы обработаны.")
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

	fmt.Printf("  🎵 Извлечена русская аудиодорожка (ID=%d) -> %s\n", rusTrack.ID, outName)
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
	output, err := cmd.Output()
	if err != nil {
		// Если команда завершилась с ошибкой, выводим stderr для диагностики
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("mkvmerge завершился с ошибкой: %s", string(ee.Stderr))
		}
		return nil, fmt.Errorf("не удалось выполнить mkvmerge: %w", err)
	}

	var data MkvIdentifyOutput
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, fmt.Errorf("не удалось разобрать JSON: %w", err)
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
