#!/usr/bin/env bash
# Test end-to-end : crée un build, suit le statut, télécharge l'APK.
# Prérequis : le serveur tourne (go run ./cmd/server) sur $BASE.
set -euo pipefail

BASE="${BASE:-http://localhost:8080}"
URL="${1:-https://news.ycombinator.com}"
APP_NAME="${2:-Hacker News}"
PACKAGE="${3:-com.exemple.hn}"

echo "▶ Création du build ($URL)…"
RESP=$(curl -sS -X POST "$BASE/api/builds" \
  -H 'Content-Type: application/json' \
  -d "{\"url\":\"$URL\",\"app_name\":\"$APP_NAME\",\"package\":\"$PACKAGE\"}")
echo "  $RESP"

ID=$(echo "$RESP" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
if [ -z "$ID" ]; then echo "✗ Pas d'id renvoyé, arrêt."; exit 1; fi
echo "  build_id = $ID"

echo "▶ Suivi du statut (Ctrl-C pour arrêter)…"
while true; do
  S=$(curl -sS "$BASE/api/builds/$ID")
  STATUS=$(echo "$S" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
  printf '\r  statut: %-10s' "$STATUS"
  case "$STATUS" in
    success) echo; break ;;
    failed)  echo; echo "✗ Échec: $S"; exit 1 ;;
  esac
  sleep 5
done

OUT="${ID}.apk"
echo "▶ Téléchargement de l'APK → $OUT"
curl -sS -L -o "$OUT" "$BASE/api/builds/$ID/apk"
echo "✓ Terminé: $(ls -lh "$OUT" | awk '{print $5, $9}')"
