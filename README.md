# mkv-merge
Склеивает видеофайлы в один mkv файл.

Нужен ffmpeg.

Запустить в папке с файлами.

something.mkv + something.mka = something_merged.mkv

```bash
GOOS=windows GOARCH=amd64 go build -o merge.exe ./merge/merge.go
```

```bash
GOOS=windows GOARCH=amd64 go build -o extract.exe ./extract/extract.go
```
