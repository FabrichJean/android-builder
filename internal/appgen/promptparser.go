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
	return fmt.Sprintf(`Génère une mini web app en UN SEUL fichier index.html complet (CSS et JS inline), sans aucune dépendance externe (pas de CDN, pas de police distante), en français.
Contraintes STRICTES :
- Réponds UNIQUEMENT avec le document HTML : la réponse commence par <!doctype html> et se termine par </html>. Aucun texte avant ou après, pas de bloc markdown.
- Code compact : vise le minimum de tokens tout en restant lisible. Pas de commentaires superflus.
- Adaptée mobile (meta viewport), utilisable hors-ligne.

Description de l'app :
%s

Précisions choisies par l'utilisateur :
%s`, desc, prefs)
}
