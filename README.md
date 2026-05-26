Файлы для замены в исходниках darkline-agent:
  internal/xray/process.go
  internal/xray/config.go
  internal/api/server.go
  Makefile

Готовый linux-amd64 бинарник:
  darkline-agent-linux-amd64

Проверка после замены исходников:
  go test ./...
  make build-linux

Установка готового бинарника на VPN-сервер:
  sudo install -m 755 darkline-agent-linux-amd64 /usr/local/bin/darkline-agent
  sudo systemctl restart darkline-agent
  sudo systemctl status darkline-agent --no-pager

Проверка API (подставить токен):
  curl -H 'X-Agent-Token: TOKEN' http://127.0.0.1:7070/health
  curl -H 'X-Agent-Token: TOKEN' http://127.0.0.1:7070/xray/status
