
Готовый linux-amd64 бинарник:
  darkline-agent-linux-amd64

Установка готового бинарника на VPN-сервер:
  sudo install -m 755 darkline-agent-linux-amd64 /usr/local/bin/darkline-agent
  sudo systemctl restart darkline-agent
  sudo systemctl status darkline-agent --no-pager

