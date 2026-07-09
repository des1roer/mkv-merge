package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ... (функции naturalLess, isDigit, extractNumber остаются без изменений) ...
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

	// 1. Сразу создаем выходную папку ОДИН РАЗ, а не в каждом воркере
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

	// Канал задач
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
		mu          sync.Mutex
		mergedCount int
	)

	// Количество одновременных потоков (воркеров)
	// Можно вынести в переменную, чтобы легко менять
	workersCount := 2

	for w := 0; w < workersCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				outputFile := filepath.Join(outputDir, fmt.Sprintf("%s.mkv", task.baseName))
				videoPath := filepath.Join(dir, task.videoFile)
				audioPath := filepath.Join(dir, task.audioFile)

				fmt.Printf("[Воркер %d] Объединение: %s + %s -> %s\n", workerID, task.videoFile, task.audioFile, filepath.Base(outputFile))

				cmd := exec.Command("ffmpeg",
					"-i", videoPath,
					"-i", audioPath,
					"-c", "copy",
					"-map", "0:v:0",
					"-map", "1:a:0",
					"-y",
					outputFile,
				)

				// ПЕРЕХВАТ ВЫВОДА: чтобы ffmpeg не спамил в консоль и не перемешивал логи
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				cmd.Stdout = nil // Нам не нужен stdout ffmpeg

				if err := cmd.Run(); err != nil {
					fmt.Printf("[Воркер %d] ❌ Ошибка при объединении %s: %v\n", workerID, task.baseName, err)
					// Выводим лог ffmpeg только если была ошибка, чтобы понять причину
					fmt.Printf("FFmpeg log:\n%s\n", stderr.String())
					continue // <-- ВАЖНО: идем к следующей задаче, а не убиваем воркер!
				}

				mu.Lock()
				mergedCount++
				mu.Unlock()
				fmt.Printf("[Воркер %d] ✅ Успешно: %s\n\n", workerID, filepath.Base(outputFile))
			}
		}(w)
	}

	wg.Wait()

	fmt.Printf("\nВсего объединено файлов: %d из %d\n", mergedCount, len(matched))
}
