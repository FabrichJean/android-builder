package appgen

import (
	"fmt"
	"regexp"
	"strings"
)

// Le prompt parser regroupe les opérations appliquées à l'entrée utilisateur
// avant tout appel de génération :
//  1. normalizeDescription — nettoyage mécanique (aucun appel IA) : balisage
//     parasite, espaces, longueur.
//  2. le filtrage anti prompt-injection / contenu interdit est fait par le
//     modèle lui-même pendant l'appel de préparation (voir prepOut.Safe dans
//     appgen.go) : on évite ainsi un appel CLI dédié qui doublerait le coût
//     en tokens pour une simple vérification.
//  3. validateAnswers — les réponses aux questions de clarification ne sont
//     acceptées QUE si elles correspondent à une option réellement proposée.
//     Sans ce filtre, le champ JSON "answers" de l'API accepterait n'importe
//     quel texte libre et contournerait entièrement la vérification de
//     sécurité faite sur la description initiale.
//  4. buildPrompt — assemble description + réponses validées en un prompt de
//     génération structuré, au lieu d'une concaténation ad hoc.

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	codeFenceRe  = regexp.MustCompile("```[a-zA-Z]*")
	multiSpaceRe = regexp.MustCompile(`[ \t]+`)
	multiBlankRe = regexp.MustCompile(`\n{3,}`)
)

// normalizeDescription nettoie mécaniquement la description utilisateur :
// retire un éventuel balisage HTML/markdown parasite, compacte les espaces
// et tronque à maxDescLen. N'effectue aucun appel réseau — c'est la seule
// étape appliquée avant même de savoir si le CLI est disponible.
func normalizeDescription(raw string) string {
	s := strings.TrimSpace(raw)
	s = htmlTagRe.ReplaceAllString(s, "")
	s = codeFenceRe.ReplaceAllString(s, "")
	s = multiSpaceRe.ReplaceAllString(s, " ")
	s = multiBlankRe.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if len(s) > maxDescLen {
		s = s[:maxDescLen]
	}
	return s
}

// validateAnswers ne conserve que les réponses correspondant EXACTEMENT à une
// option proposée pour la question correspondante ; toute valeur libre
// envoyée hors de ce cadre est silencieusement ignorée.
func validateAnswers(questions []Question, answers map[string]string) map[string]string {
	clean := make(map[string]string, len(questions))
	for _, q := range questions {
		a := answers[q.ID]
		for _, opt := range q.Options {
			if a == opt {
				clean[q.ID] = a
				break
			}
		}
	}
	return clean
}

// buildPrompt assemble la description et les réponses validées en un prompt
// de génération structuré et stable.
func buildPrompt(desc string, questions []Question, answers map[string]string) string {
	clean := validateAnswers(questions, answers)
	var sb strings.Builder
	for _, q := range questions {
		if a := clean[q.ID]; a != "" {
			fmt.Fprintf(&sb, "- %s → %s\n", q.Label, a)
		}
	}
	prefs := sb.String()
	if prefs == "" {
		prefs = "(aucune précision supplémentaire)\n"
	}
	return fmt.Sprintf(`Tu es un développeur front-end senior. Génère une mini web app sous forme de PROJET STATIQUE BIEN STRUCTURÉ, en français, sans aucune dépendance externe (pas de CDN, pas de police distante, pas de build), utilisable hors-ligne dans une WebView Android.

FORMAT DE SORTIE (strict) :
- Chaque fichier est précédé d'une ligne exacte "=== FILE: chemin/du/fichier ===", suivie directement de son contenu. Aucun autre texte avant, entre ou après, aucun bloc markdown.
- Chemins relatifs en minuscules (lettres, chiffres, - _ .), extensions autorisées : html, css, js, json, svg. 12 fichiers maximum.
- Le premier fichier est index.html (document complet commençant par <!doctype html>).

STRUCTURE DU PROJET (adapte le nombre de fichiers à la taille de l'app : 3 pour une petite, 5 à 7 pour une app riche) :
- index.html : uniquement le balisage sémantique (header/main/nav/section…), <meta viewport>, <link rel="stylesheet" href="css/style.css"> et <script type="module" src="js/main.js"></script>. Pas de CSS ni de JS inline.
- css/style.css : variables CSS (:root) pour couleurs/espacements/rayons, approche mobile-first, sections commentées (base, layout, composants). Respecte env(safe-area-inset-*), cibles tactiles ≥ 44px, contrastes lisibles, prefers-color-scheme si pertinent.
- js/main.js : point d'entrée qui initialise l'app et branche les événements, rien d'autre.
- js/state.js (ou data.js) : l'état de l'app et sa logique métier PURE (aucun accès au DOM).
- js/storage.js : persistance localStorage (clé versionnée, try/catch, valeurs par défaut) si l'app doit garder des données.
- js/ui.js (ou views.js) : rendu et manipulation du DOM à partir de l'état.
- Ajoute d'autres modules (ex. js/utils.js, js/game.js) seulement s'ils ont une responsabilité claire.

RÈGLES DE QUALITÉ DU CODE :
- JavaScript ES modules (import/export), const/let, jamais var ; une responsabilité par fichier et par fonction ; fonctions courtes aux noms explicites ; constantes nommées plutôt que valeurs magiques.
- Sépare l'état, la logique et le rendu : une source de vérité unique, une fonction render(état) ; délégation d'événements plutôt que des écouteurs en cascade.
- Jamais d'innerHTML avec du texte saisi par l'utilisateur (utilise textContent / createElement) ; valide les entrées ; gère les cas vides et les erreurs sans planter.
- Accessibilité : éléments natifs (button, label, etc.), aria-label sur les boutons-icônes, focus visible.
- Code compact (le budget de tokens est limité) mais lisible : une ligne d'en-tête en commentaire par fichier et de brefs commentaires de section, pas de commentaires superflus.

Description de l'app :
%s

Précisions choisies par l'utilisateur :
%s`, desc, prefs)
}
