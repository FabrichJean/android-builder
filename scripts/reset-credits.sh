#!/bin/sh
# Réinitialise les crédits journaliers de génération IA (table appgen_credits).
# Une ligne absente pour le jour courant = budget complet, donc "réinitialiser"
# revient simplement à supprimer la ligne du jour de l'utilisateur.
#
# Usage :
#   scripts/reset-credits.sh --list            # affiche la conso du jour par utilisateur
#   scripts/reset-credits.sh <user_id>         # remet les crédits du jour de cet utilisateur
#   scripts/reset-credits.sh --all             # remet les crédits du jour de tout le monde
#
# La base est celle du serveur : $DB_PATH, sinon data.db à la racine du projet.
set -eu

DB="${DB_PATH:-$(dirname "$0")/../data.db}"
TODAY=$(date +%F)

if [ ! -f "$DB" ]; then
	echo "base introuvable : $DB (définis DB_PATH si elle est ailleurs)" >&2
	exit 1
fi

case "${1:-}" in
"")
	echo "usage : $0 --list | --all | <user_id>" >&2
	exit 1
	;;
--list)
	echo "Consommation du $TODAY (user_id | tokens utilisés) :"
	sqlite3 "$DB" "SELECT user_id || ' | ' || tokens_used FROM appgen_credits WHERE day = '$TODAY' ORDER BY tokens_used DESC;"
	;;
--all)
	N=$(sqlite3 "$DB" "SELECT count(*) FROM appgen_credits WHERE day = '$TODAY';")
	sqlite3 "$DB" "DELETE FROM appgen_credits WHERE day = '$TODAY';"
	echo "crédits du $TODAY réinitialisés pour $N utilisateur(s)"
	;;
*)
	USER_ID=$1
	USED=$(sqlite3 "$DB" "SELECT tokens_used FROM appgen_credits WHERE user_id = '$USER_ID' AND day = '$TODAY';")
	if [ -z "$USED" ]; then
		echo "aucune consommation aujourd'hui pour $USER_ID — crédits déjà complets"
		exit 0
	fi
	sqlite3 "$DB" "DELETE FROM appgen_credits WHERE user_id = '$USER_ID' AND day = '$TODAY';"
	echo "crédits réinitialisés pour $USER_ID ($USED tokens remis)"
	;;
esac
