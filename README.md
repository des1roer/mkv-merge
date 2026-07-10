# mkv-merge
Склеивает видеофайлы в один mkv файл.

Нужен ffmpeg.

Запустить в папке с файлами.

something.mkv + something.mka = something_merged.mkv

```bash
GOOS=windows GOARCH=amd64 go build -o merge-v2.exe main.go
```

```bash
GOOS=windows GOARCH=amd64 go build -o extract.exe ./extract/
```
