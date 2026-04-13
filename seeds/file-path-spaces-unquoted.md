---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [shell]
keywords: [path, spaces, quote, macos, file, directory]
severity: minor
recurrence: 0
root-cause: "Shell разбивает аргументы по пробелам, поэтому путь 'My Documents/file.txt' без кавычек становится двумя аргументами: 'My' и 'Documents/file.txt'"
prevention: "Всегда оборачивать пути в двойные кавычки, особенно в скриптах и командах копирования"
decay_rate: never
ref_count: 0
triggers:
  - "path spaces"
  - "unquoted path"
scope: domain
---

# Пути с пробелами без кавычек

## Симптом
```bash
cp /Users/me/My Documents/file.txt /tmp/
# cp: cannot stat '/Users/me/My': No such file or directory
# cp: cannot stat 'Documents/file.txt': No such file or directory
```

Особенно часто на macOS, где `~/Documents`, `~/Desktop`, `~/Library/Application Support` — все с пробелами.

## Почему
Shell разбивает командную строку по whitespace **до** того, как команда её увидит. `cp` получает три аргумента вместо двух: `My`, `Documents/file.txt`, `/tmp/`.

## Fix
Всегда кавычки:
```bash
cp "/Users/me/My Documents/file.txt" /tmp/
```

В переменных:
```bash
SRC="$HOME/My Documents/file.txt"
cp "$SRC" /tmp/    # Кавычки И при объявлении, И при использовании
```

В скриптах с `find`:
```bash
find "$DIR" -name "*.txt" -exec cp "{}" /tmp/ \;
```

## Грабли
- Tab-completion в zsh/bash сам экранирует пробелы (`\ `), но при копипасте экранирование теряется
- `$@` vs `"$@"` в скриптах: первое разбивает аргументы по пробелам, второе сохраняет
- IFS (Internal Field Separator) можно перенастроить, но это редко правильное решение
