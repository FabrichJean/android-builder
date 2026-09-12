#!/usr/bin/env bash
# Lancé par pm2 (ecosystem.config.js) à chaque start/restart.
# Recompile avant de démarrer ; si le build échoue, garde l'ancien binaire
# plutôt que de planter le service.
set -euo pipefail
cd /www/android-builder

if go build -o bin/android-builder-server.new ./cmd/server; then
  mv bin/android-builder-server.new bin/android-builder-server
else
  echo "⚠ build échoué, on garde le binaire précédent" >&2
  rm -f bin/android-builder-server.new
fi

exec ./bin/android-builder-server
