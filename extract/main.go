package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AudioStream описывает аудиопоток из ffprobe
type AudioStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec_name"`
	Language string `json:"language,omitempty"`
	Tags     struct {
		Language string `json:"language,omitempty"`
	} `json:"tags,omitempty"`
}

// FFProbeOutput структура для парсинга JSON вывода ffprobe
type FFProbeOutput struct {
	Streams []AudioStream `json:"streams"`
}

func main() {
	// Определяем директорию, где находится исполняемый файл
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Ошибка получения пути к исполняемому файлу: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Dir(execPath)
	fmt.Printf("Рабочая директория: %s\n", baseDir)

	// Проверяем наличие ffprobe и ffmpeg
	if _, err := exec.LookPath("ffprobe"); err != nil {
		fmt.Println("ffprobe не найден в PATH. Установите ffmpeg.")
		os.Exit(1)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("ffmpeg не найден в PATH. Установите ffmpeg.")
		os.Exit(1)
	}

	// Создаём папку output внутри baseDir
	outputDir := filepath.Join(baseDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Не удалось создать папку output: %v\n", err)
		os.Exit(1)
	}

	// Обходим все файлы в baseDir (не рекурсивно, только корень)
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		fmt.Printf("Ошибка чтения директории: %v\n", err)
		os.Exit(1)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if !isVideoFile(filename) {
			continue
		}
		filePath := filepath.Join(baseDir, filename)
		fmt.Printf("Обработка: %s\n", filename)
		if err := processVideo(filePath, outputDir); err != nil {
			fmt.Printf("  Ошибка обработки %s: %v\n", filename, err)
		}
	}
	fmt.Println("Готово.")
}

// isVideoFile проверяет расширение файла
func isVideoFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	videoExts := map[string]bool{
		".mp4": true, ".avi": true, ".mkv": true, ".mov": true,
		".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
		".mpg": true, ".mpeg": true,
	}
	return videoExts[ext]
}

// processVideo извлекает все аудиодорожки из видеофайла
func processVideo(videoPath, outputDir string) error {
	// Получаем информацию об аудиопотоках через ffprobe
	streams, err := getAudioStreams(videoPath)
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	if len(streams) == 0 {
		fmt.Println("  Аудиопотоков не найдено.")
		return nil
	}

	// Базовое имя файла без расширения
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

	for _, stream := range streams {
		lang := stream.Language
		if lang == "" {
			if stream.Tags.Language != "" {
				lang = stream.Tags.Language
			} else {
				lang = "und" // unknown
			}
		}
		// Формируем имя выходного файла: base_audio<index>_<lang>.mka
		outName := fmt.Sprintf("%s_audio%d_%s.mka", base, stream.Index, lang)
		outPath := filepath.Join(outputDir, outName)

		// Извлекаем аудиопоток с копированием без перекодирования
		if err := extractAudioStream(videoPath, stream.Index, outPath); err != nil {
			return fmt.Errorf("извлечение потока %d: %w", stream.Index, err)
		}
		fmt.Printf("  Извлечена дорожка %d (язык: %s) -> %s\n", stream.Index, lang, outName)
	}
	return nil
}

// getAudioStreams запускает ffprobe и возвращает список аудиопотоков с языками
func getAudioStreams(videoPath string) ([]AudioStream, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "a",
		videoPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe execution: %w", err)
	}

	var data FFProbeOutput
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, fmt.Errorf("json parse: %w", err)
	}
	return data.Streams, nil
}

// extractAudioStream извлекает один аудиопоток в файл с помощью ffmpeg (копирование)
func extractAudioStream(videoPath string, streamIndex int, outPath string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-i", videoPath,
		"-map", fmt.Sprintf("0:a:%d", streamIndex),
		"-c", "copy",
		"-y", // перезаписывать существующий
		outPath,
	)
	// Для подавления вывода можно перенаправить в nil, но оставим для отладки
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
