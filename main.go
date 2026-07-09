package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		aDigit := isDigit(ca)
		bDigit := isDigit(cb)

		if aDigit && bDigit {
			numA, endA := extractNumber(a, i)
			numB, endB := extractNumber(b, j)
			if numA != numB {
				return numA < numB
			}
			i, j = endA, endB
			continue
		}

		if aDigit != bDigit {
			return aDigit
		}

		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func extractNumber(s string, start int) (int, int) {
	end := start
	for end < len(s) && isDigit(s[end]) {
		end++
	}
	n, _ := strconv.Atoi(s[start:end])
	return n, end
}

func getDuration(filePath string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

// createProgressBar создает текстовый прогресс-бар
func createProgressBar(current, total int64, width int) string {
	if total == 0 {
		return "[?]"
	}
	percent := float64(current) / float64(total)
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %3d%%", bar, int(percent*100))
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	} else {
		exePath, err := os.Executable()
		if err != nil {
			fmt.Printf("Ошибка получения пути к exe: %v\n", err)
			os.Exit(1)
		}
		dir = filepath.Dir(exePath)
	}

	fmt.Printf("Рабочая директория: %s\n\n", dir)

	outputDir := filepath.Join(dir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Ошибка при создании директории %s: %v\n", outputDir, err)
		os.Exit(1)
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		fmt.Printf("Ошибка чтения директории: %v\n", err)
		os.Exit(1)
	}

	videoFiles := make(map[string]string)
	audioFiles := make(map[string]string)

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		ext := strings.ToLower(filepath.Ext(name))
		baseName := strings.TrimSuffix(name, ext)

		switch ext {
		case ".mkv":
			videoFiles[baseName] = name
		case ".mka":
			audioFiles[baseName] = name
		}
	}

	var matched []string
	for baseName := range videoFiles {
		if _, exists := audioFiles[baseName]; exists {
			matched = append(matched, baseName)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return naturalLess(matched[i], matched[j])
	})

	if len(matched) == 0 {
		fmt.Println("Не найдено пар файлов для объединения")
		return
	}

	type Task struct {
		baseName  string
		videoFile string
		audioFile string
	}
	tasks := make(chan Task, len(matched))

	for _, baseName := range matched {
		tasks <- Task{baseName, videoFiles[baseName], audioFiles[baseName]}
	}
	close(tasks)

	var (
		wg          sync.WaitGroup
		mergedCount int
		printMu     sync.Mutex // Мьютекс для синхронизации вывода
	)

	totalFiles := len(matched)
	workersCount := 2

	// Хранилище статусов воркеров
	type WorkerStatus struct {
		CurrentFile string
		Progress    int64
		Total       int64
		Active      bool
	}
	workerStatuses := make([]WorkerStatus, workersCount)

	// Горутина для обновления прогресс-баров
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printMu.Lock()
				// Очищаем экран (опционально)
				fmt.Print("\033[H\033[2J")

				// Общий прогресс
				completed := 0
				for _, status := range workerStatuses {
					if !status.Active && status.Total > 0 {
						completed++
					}
				}
				fmt.Printf("Общий прогресс: %d/%d файлов\n", completed, totalFiles)
				fmt.Println(strings.Repeat("-", 60))

				// Прогресс каждого воркера
				for i, status := range workerStatuses {
					if status.Active {
						bar := createProgressBar(status.Progress, status.Total, 40)
						fmt.Printf("Воркер %d: %s %s\n", i+1, bar, status.CurrentFile)
					} else {
						fmt.Printf("Воркер %d: Ожидание задачи...\n", i+1)
					}
				}
				printMu.Unlock()
			case <-stopProgress:
				return
			}
		}
	}()

	for w := 0; w < workersCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				outputFile := filepath.Join(outputDir, fmt.Sprintf("%s.mkv", task.baseName))
				videoPath := filepath.Join(dir, task.videoFile)
				audioPath := filepath.Join(dir, task.audioFile)

				duration, err := getDuration(videoPath)
				if err != nil {
					duration = 0
				}

				// Обновляем статус воркера
				printMu.Lock()
				workerStatuses[workerID] = WorkerStatus{
					CurrentFile: task.baseName,
					Progress:    0,
					Total:       int64(duration),
					Active:      true,
				}
				printMu.Unlock()

				cmd := exec.Command("ffmpeg",
					"-i", videoPath,
					"-i", audioPath,
					"-c", "copy",
					"-map", "0:v:0",
					"-map", "1:a:0",
					"-progress", "pipe:1",
					"-nostats",
					"-y",
					outputFile,
				)

				stdout, err := cmd.StdoutPipe()
				if err != nil {
					continue
				}

				var stderr bytes.Buffer
				cmd.Stderr = &stderr

				if err := cmd.Start(); err != nil {
					continue
				}

				scanner := bufio.NewScanner(stdout)
				timeRegex := regexp.MustCompile(`out_time_ms=(\d+)`)

				go func() {
					for scanner.Scan() {
						line := scanner.Text()
						if matches := timeRegex.FindStringSubmatch(line); len(matches) > 1 && duration > 0 {
							timeMs, _ := strconv.ParseInt(matches[1], 10, 64)
							timeSec := timeMs / 1000000

							printMu.Lock()
							workerStatuses[workerID].Progress = timeSec
							printMu.Unlock()
						}
					}
				}()

				if err := cmd.Wait(); err != nil {
					continue
				}

				// Завершаем задачу
				printMu.Lock()
				workerStatuses[workerID] = WorkerStatus{
					CurrentFile: task.baseName,
					Progress:    int64(duration),
					Total:       int64(duration),
					Active:      false,
				}
				mergedCount++
				printMu.Unlock()
			}
		}(w)
	}

	wg.Wait()
	close(stopProgress)

	// Финальный вывод
	fmt.Print("\033[H\033[2J")
	fmt.Printf("✅ Всего объединено файлов: %d из %d\n", mergedCount, totalFiles)
}
