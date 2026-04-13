---
type: mistake
seed: true
created: 2026-04-13
status: active
domains: [shell]
keywords: [bash, shell, escape, dollar, exclamation, quote, password]
severity: minor
recurrence: 0
root-cause: "Спецсимволы ($, !, обратные кавычки, $!) интерпретируются shell внутри двойных кавычек до того, как строка попадёт в команду"
prevention: "Использовать одинарные кавычки для строк со спецсимволами; для bcrypt/паролей — heredoc с EOF без интерполяции"
decay_rate: never
ref_count: 0
triggers:
  - "shell escape"
  - "bash dollar sign"
scope: domain
---

# Shell escape: спецсимволы в кавычках

## Симптом
Команда падает с непонятной ошибкой или выполняется с искажённой строкой:
```bash
echo "P@ssw0rd$abc!"   # → P@ssw0rd!  (переменная $abc пустая, ! может вызвать history expansion)
```

## Почему
Двойные кавычки в bash **интерпретируют**:
- `$VAR` — подстановка переменной
- `$(...)` и `` `...` `` — выполнение команды
- `\n`, `\t` и т.д. в некоторых контекстах
- `!` — history expansion в интерактивной shell

## Fix
Одинарные кавычки **не интерпретируют ничего**:
```bash
echo 'P@ssw0rd$abc!'   # → P@ssw0rd$abc!
```

Для длинных строк/паролей — heredoc с одинарным `EOF`:
```bash
cat <<'EOF' > /tmp/secret
P@ssw0rd$abc!`anything`
EOF
```

## Когда это критично
- Bcrypt-хеши (`$2b$12$...` — `$12` интерпретируется как переменная)
- Пароли при `curl -u user:pass`
- JSON-payloads с `$` (jq queries, MongoDB syntax)
