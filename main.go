package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	dir := "."

	if len(os.Args) > 1 {
		dir = os.Args[1]
	} else {
		// Если аргумент не передан - используем директорию exe
		exePath, err := os.Executable()
		if err != nil {
			fmt.Printf("Ошибка получения пути к exe: %v\n", err)
			os.Exit(1)
		}
		dir = filepath.Dir(exePath)
	}

	fmt.Printf("Рабочая директория: %s\n\n", dir)

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

	mergedCount := 0
	for baseName, videoFile := range videoFiles {
		if audioFile, exists := audioFiles[baseName]; exists {
			outputFile := filepath.Join(dir, fmt.Sprintf("%s_merged.mkv", baseName))
			videoPath := filepath.Join(dir, videoFile)
			audioPath := filepath.Join(dir, audioFile)

			fmt.Printf("Объединение: %s + %s -> %s\n", videoFile, audioFile, filepath.Base(outputFile))

			cmd := exec.Command("ffmpeg",
				"-i", videoPath,
				"-i", audioPath,
				"-c", "copy",
				"-map", "0:v:0",
				"-map", "1:a:0",
				"-y",
				outputFile,
			)

			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Run(); err != nil {
				fmt.Printf("Ошибка при объединении %s: %v\n", baseName, err)
				continue
			}

			mergedCount++
			fmt.Printf("✓ Успешно: %s\n\n", filepath.Base(outputFile))
		}
	}

	if mergedCount == 0 {
		fmt.Println("Не найдено пар файлов для объединения")
	} else {
		fmt.Printf("\nВсего объединено файлов: %d\n", mergedCount)
	}
}
