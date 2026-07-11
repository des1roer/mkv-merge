package main

import (
	"bufio"
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

// naturalLess для естественной сортировки
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

// createProgressBar создает текстовый прогресс-бар
func createProgressBar(percent int, width int) string {
	if percent > 100 {
		percent = 100
	}
	filled := int(float64(percent) / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %3d%%", bar, percent)
}

func main() {
	// Определяем директорию
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

	// Проверяем наличие mkvmerge
	if _, err := exec.LookPath("mkvmerge"); err != nil {
		fmt.Println("Ошибка: mkvmerge не найден в PATH. Установите MKVToolNix.")
		os.Exit(1)
	}

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
		printMu     sync.Mutex
	)

	totalFiles := len(matched)
	workersCount := 2

	type WorkerStatus struct {
		CurrentFile string
		Percent     int // 0-100
		Active      bool
	}
	workerStatuses := make([]WorkerStatus, workersCount)

	// Горутина для обновления прогресс-баров
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printMu.Lock()
				// Очищаем экран
				fmt.Print("\033[H\033[2J")

				completed := 0
				for _, status := range workerStatuses {
					if !status.Active && status.Percent >= 100 {
						completed++
					}
				}
				fmt.Printf("Общий прогресс: %d/%d файлов\n", completed, totalFiles)
				fmt.Println(strings.Repeat("-", 60))

				for i, status := range workerStatuses {
					if status.Active {
						bar := createProgressBar(status.Percent, 40)
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

				// Обновляем статус воркера
				printMu.Lock()
				workerStatuses[workerID] = WorkerStatus{
					CurrentFile: task.baseName,
					Percent:     0,
					Active:      true,
				}
				printMu.Unlock()

				// Команда mkvmerge: берём видео из первого файла (0:0) и аудио из второго (1:0)
				cmd := exec.Command("mkvmerge",
					"-o", outputFile,
					"--video-tracks", "0:0",
					"--audio-tracks", "1:0",
					videoPath,
					audioPath,
				)

				// Захватываем stderr для прогресса
				stderr, err := cmd.StderrPipe()
				if err != nil {
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}

				// Запускаем
				if err := cmd.Start(); err != nil {
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}

				// Читаем stderr построчно и ищем прогресс
				scanner := bufio.NewScanner(stderr)
				progressRegex := regexp.MustCompile(`Progress:\s*(\d+)%`)

				go func() {
					for scanner.Scan() {
						line := scanner.Text()
						if matches := progressRegex.FindStringSubmatch(line); len(matches) > 1 {
							p, _ := strconv.Atoi(matches[1])
							printMu.Lock()
							if p > workerStatuses[workerID].Percent {
								workerStatuses[workerID].Percent = p
							}
							printMu.Unlock()
						}
					}
				}()

				// Ждём завершения
				if err := cmd.Wait(); err != nil {
					// Ошибка, но всё равно помечаем как завершённое
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}

				// Завершено успешно
				printMu.Lock()
				workerStatuses[workerID] = WorkerStatus{
					CurrentFile: task.baseName,
					Percent:     100,
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
