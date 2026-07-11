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

var logFile *os.File
var logMu sync.Mutex

func setupLog(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "out.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	logFile = f
}

func lprint(s string) {
	fmt.Print(s)
	logMu.Lock()
	if logFile != nil {
		logFile.WriteString(s)
	}
	logMu.Unlock()
}

func lprintf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	lprint(s)
}

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

func getDuration(filePath string) float64 {
	cmd := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", filePath)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return dur
}

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	} else {
		exePath, err := os.Executable()
		if err != nil {
			lprintf("Ошибка получения пути к exe: %v\n", err)
			os.Exit(1)
		}
		dir = filepath.Dir(exePath)
	}

	setupLog(dir)
	if logFile != nil {
		defer logFile.Close()
	}

	lprintf("Рабочая директория: %s\n\n", dir)

	outputDir := filepath.Join(dir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		lprintf("Ошибка при создании директории %s: %v\n", outputDir, err)
		os.Exit(1)
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		lprintf("Ошибка чтения директории: %v\n", err)
		os.Exit(1)
	}

	videoFiles := make(map[string]string)
	audioFiles := make(map[string]string)
	videoExt := make(map[string]string)

	videoExts := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
		".webm": true, ".m4v": true, ".wmv": true, ".flv": true,
	}
	audioExts := map[string]bool{
		".mka": true, ".m4a": true, ".aac": true, ".ac3": true,
		".eac3": true, ".dts": true, ".wav": true, ".mp3": true,
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		ext := strings.ToLower(filepath.Ext(name))
		baseName := strings.TrimSuffix(name, ext)
		if videoExts[ext] {
			videoFiles[baseName] = name
			videoExt[baseName] = ext
		}
		if audioExts[ext] {
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
		lprint("Не найдено пар файлов для объединения\n")
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

	type WorkerStatus struct {
		CurrentFile string
		Percent     int
		Active      bool
	}

	workersCount := 2
	workerStatuses := make([]WorkerStatus, workersCount)
	totalFiles := len(matched)

	var (
		wg          sync.WaitGroup
		mergedCount int
		printMu     sync.Mutex
	)

	// Progress bar — overwrites line with \r
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printMu.Lock()
				fmt.Print("\r")
				completed := 0
				for _, status := range workerStatuses {
					if !status.Active && status.Percent >= 100 {
						completed++
					}
				}
				s := fmt.Sprintf("%d/%d | ", completed, totalFiles)
				for i, status := range workerStatuses {
					if i > 0 {
						s += " | "
					}
					if status.Active {
						bar := createProgressBar(status.Percent, 25)
						s += fmt.Sprintf("W%d: %s %s", i+1, bar, status.CurrentFile)
					} else {
						s += fmt.Sprintf("W%d: idle", i+1)
					}
				}
				fmt.Print(s + "\r")
				printMu.Unlock()
			case <-stopProgress:
				return
			}
		}
	}()

	progRe := regexp.MustCompile(`out_time_ms=(\d+)`)

	for w := 0; w < workersCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				outputExt := videoExt[task.baseName]
				if outputExt == "" {
					outputExt = ".mkv"
				}
				outputFile := filepath.Join(outputDir, fmt.Sprintf("%s%s", task.baseName, outputExt))
				videoPath := filepath.Join(dir, task.videoFile)
				audioPath := filepath.Join(dir, task.audioFile)

				totalDur := getDuration(videoPath)

				printMu.Lock()
				workerStatuses[workerID] = WorkerStatus{
					CurrentFile: task.baseName,
					Percent:     0,
					Active:      true,
				}
				printMu.Unlock()

				cmd := exec.Command("ffmpeg",
					"-y", "-i", videoPath, "-i", audioPath,
					"-c", "copy",
					"-map", "0:v", "-map", "0:s", "-map", "1:a",
					"-map_metadata", "0",
					"-progress", "pipe:1", "-nostats",
					outputFile,
				)

				stdout, err := cmd.StdoutPipe()
				if err != nil {
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}
				var stderrOut bytes.Buffer
				cmd.Stderr = &stderrOut

				if err := cmd.Start(); err != nil {
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}

				go func() {
					scanner := bufio.NewScanner(stdout)
					for scanner.Scan() {
						line := scanner.Text()
						if matches := progRe.FindStringSubmatch(line); len(matches) > 1 && totalDur > 0 {
							timeMs, _ := strconv.ParseInt(matches[1], 10, 64)
							pct := int(float64(timeMs) / 1e6 / totalDur * 100)
							if pct > 99 {
								pct = 99
							}
							printMu.Lock()
							if pct > workerStatuses[workerID].Percent {
								workerStatuses[workerID].Percent = pct
							}
							printMu.Unlock()
						}
					}
				}()

				if err := cmd.Wait(); err != nil {
					fmt.Printf("\n❌ Ошибка: %s — %v\n", task.baseName, err)
					if stderrOut.Len() > 0 {
						lines := strings.Split(strings.TrimSpace(stderrOut.String()), "\n")
						if lastLine := lines[len(lines)-1]; lastLine != "" {
							fmt.Printf("   stderr: %s\n", lastLine)
						}
					}
					printMu.Lock()
					workerStatuses[workerID].Active = false
					printMu.Unlock()
					continue
				}

				fmt.Printf("\n✅ Завершено: %s\n", task.baseName)
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

	fmt.Printf("\n✅ Всего объединено файлов: %d из %d\n", mergedCount, totalFiles)
}
