package util

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

func NaturalLess(a, b string) bool {
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

func GetDuration(filePath string) (float64, error) {
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

func CreateProgressBar(current, total int64, width int) string {
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

type AudioStreamInfo struct {
	Index    int
	Language string
	Codec    string
}

func GetAudioStreams(filePath string) ([]AudioStreamInfo, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=index,stream=language,stream=codec_name",
		"-of", "csv=p=0:s=:_",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var streams []AudioStreamInfo
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := 0; i < len(lines)-2; i += 3 {
		index, _ := strconv.Atoi(strings.TrimSpace(lines[i]))
		lang := strings.TrimSpace(lines[i+1])
		codec := strings.TrimSpace(lines[i+2])
		if lang == "" {
			lang = "und"
		}
		streams = append(streams, AudioStreamInfo{Index: index, Language: lang, Codec: codec})
	}
	return streams, nil
}

func GetDir(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("Ошибка получения пути к exe: %v\n", err)
		os.Exit(1)
	}
	return filepath.Dir(exePath)
}

type WorkerStatus struct {
	CurrentFile string
	Progress    int64
	Total       int64
	Active      bool
}

// RunProgressBar starts a goroutine that refreshes progress bars every 500ms.
// Returns a channel to stop it.
func RunProgressBar(
	workersCount int, totalItems int, label string,
	workerStatuses []WorkerStatus, printMu *sync.Mutex,
) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				printMu.Lock()
				fmt.Print("\033[H\033[2J")
				completed := 0
				for _, status := range workerStatuses {
					if !status.Active && status.Total > 0 {
						completed++
					}
				}
				fmt.Printf("%s: %d/%d\n", label, completed, totalItems)
				fmt.Println(strings.Repeat("-", 60))
				for i, status := range workerStatuses {
					if status.Active {
						bar := CreateProgressBar(status.Progress, status.Total, 40)
						fmt.Printf("Воркер %d: %s %s\n", i+1, bar, status.CurrentFile)
					} else {
						fmt.Printf("Воркер %d: Ожидание задачи...\n", i+1)
					}
				}
				printMu.Unlock()
			case <-stop:
				return
			}
		}
	}()
	return stop
}

// RunFFmpegProgress starts an ffmpeg command and updates progress via stdout parsing.
func RunFFmpegProgress(
	cmd *exec.Cmd,
	workerID int,
	workerStatuses *[]WorkerStatus,
	printMu *sync.Mutex,
	duration float64,
) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return err
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
				(*workerStatuses)[workerID].Progress = timeSec
				printMu.Unlock()
			}
		}
	}()

	return cmd.Wait()
}

func SortStringsByNatural(s []string) {
	sort.Slice(s, func(i, j int) bool {
		return NaturalLess(s[i], s[j])
	})
}

func VideoExtensions() map[string]bool {
	return map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
		".webm": true, ".m4v": true, ".wmv": true, ".flv": true,
	}
}

// FindVideoAudioFiles reads a directory and returns .mkv and .mka files keyed by base name.
func FindVideoAudioFiles(dir string) (map[string]string, map[string]string) {
	videoFiles := make(map[string]string)
	audioFiles := make(map[string]string)

	files, err := os.ReadDir(dir)
	if err != nil {
		return videoFiles, audioFiles
	}

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
	return videoFiles, audioFiles
}

// MatchedPairs returns sorted base names that exist in both maps.
func MatchedPairs(videoFiles, audioFiles map[string]string) []string {
	var matched []string
	for baseName := range videoFiles {
		if _, exists := audioFiles[baseName]; exists {
			matched = append(matched, baseName)
		}
	}
	SortStringsByNatural(matched)
	return matched
}

// VideoFilesWithAudio finds video files in dir that have audio streams, sorted naturally.
type VideoFileInfo struct {
	FileName string
	Streams  []AudioStreamInfo
}

func VideoFilesWithAudio(dir string) []VideoFileInfo {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	exts := VideoExtensions()
	var result []VideoFileInfo

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(file.Name()))
		if !exts[ext] {
			continue
		}

		streams, err := GetAudioStreams(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		if len(streams) == 0 {
			continue
		}
		result = append(result, VideoFileInfo{FileName: file.Name(), Streams: streams})
	}

	sort.Slice(result, func(i, j int) bool {
		return NaturalLess(result[i].FileName, result[j].FileName)
	})
	return result
}
