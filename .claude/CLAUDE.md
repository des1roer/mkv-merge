# MKV-Merge

## Описание

Утилита для работы с видеофайлами — извлечение русских аудиодорожек и их объединение с видео.

## Структура

- `extract/` — извлекает русскую аудиодорожку из MKV/MP4 файлов с помощью MKVToolNix (`mkvmerge`, `mkvextract`)
- `merge/` — объединяет видео и аудио файлы с совпадающими именами с помощью FFmpeg
- `output/` — директория для результатов (создаётся автоматически)

## Зависимости

- Go 1.26+ (только stdlib)
- MKVToolNix (extract)
- FFmpeg (merge)

## Build

```bash
go build -o extract.exe ./extract
go build -o merge.exe ./merge
```
