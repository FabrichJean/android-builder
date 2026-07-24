# android-builder

Serveur Go qui construit un **APK Android WebView** à partir d'une simple URL, en
déléguant la compilation à un **workflow GitHub Actions**. On envoie une URL + un
nom + un package, on récupère un APK installable qui affiche le site en plein écran.

## Architecture

```
Client ──POST /api/builds──▶ Serveur Go ──workflow_dispatch──▶ GitHub Actions
       ◀─── build_id ───────            ◀── suivi du run ────   (Gradle → APK)
       ──GET /api/builds/{id}──▶  (poll statut)
       ──GET .../apk──▶ APK ◀── artifact du run ────────────────
```

- **Serveur Go** (`cmd/server`, `internal/*`) : API HTTP, déclenche le workflow,
  suit l'exécution du run et récupère l'APK depuis l'artifact.
- **Workflow** (`.github/workflows/build-apk.yml`) : reçoit les paramètres,
  injecte URL/nom/package dans le template, compile un APK debug, l'expose en artifact.
- **Template Android** (`android-template/`) : projet Gradle minimal avec une
  `Activity` WebView (JS activé, gestion du bouton retour, sauvegarde d'état).

Corrélation serveur ↔ run : le serveur génère un `build_id`, le workflow nomme son
run `apk-<build_id>` (`run-name`), et le serveur retrouve le run par ce nom.

## Prérequis

1. Un dépôt GitHub contenant **ce projet** (workflow + `android-template/`).
2. Un token GitHub (PAT classique ou fine-grained) avec **Actions: read and write**
   sur ce dépôt.
3. Go 1.26+ pour lancer le serveur (ou Docker).

## Configuration

Copier `.env.example` puis renseigner les variables :

| Variable          | Rôle                                             | Défaut         |
|-------------------|--------------------------------------------------|----------------|
| `PORT`            | Port HTTP                                         | `8080`         |
| `GITHUB_TOKEN`    | Token avec droit Actions RW                       | —              |
| `GITHUB_OWNER`    | Propriétaire du dépôt                             | —              |
| `GITHUB_REPO`     | Nom du dépôt                                      | —              |
| `GITHUB_WORKFLOW` | Fichier de workflow                              | `build-apk.yml`|
| `GITHUB_REF`      | Branche de build                                 | `main`         |
| `RELEASES_DIR`    | Dossier où sont écrits les APK produits          | `releases`     |

## Lancer

```bash
export $(grep -v '^#' .env | xargs)   # ou: source .env
go run ./cmd/server
```

Puis ouvre **http://localhost:8080/** : une interface web (thème sombre sableux)
permet de saisir une URL, un nom et un package, de lancer le build et de suivre
son avancement jusqu'au téléchargement de l'APK. L'API reste utilisable directement
(voir plus bas).

Ou via Docker :

```bash
docker build -t android-builder .
docker run --rm -p 8080:8080 --env-file .env android-builder
```

## API

### Créer un build

```bash
curl -X POST http://localhost:8080/api/builds \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://news.ycombinator.com",
    "app_name": "Hacker News",
    "package": "com.exemple.hn",
    "version_name": "1.0",
    "version_code": "1"
  }'
# → 202 {"id":"a1b2c3...","status":"pending"}
```

Champs : `url` (http/https, requis), `app_name` (requis), `package`
(`com.exemple.app`, requis), `version_name` (déf. `1.0`), `version_code`
(entier, déf. `1`).

### Suivre le statut

```bash
curl http://localhost:8080/api/builds/a1b2c3...
# → {"id":"...","status":"building","run_url":"https://github.com/.../actions/runs/123", ...}
```

Statuts : `pending` → `building` → `success` (ou `failed`, voir champ `error`).

### Télécharger l'APK

```bash
curl -L -o app.apk http://localhost:8080/api/builds/a1b2c3.../apk
```

Chaque APK réussi est aussi écrit sur disque dans le dossier `releases/`
(configurable via `RELEASES_DIR`), nommé `<nom-app>-<build_id>.apk`. Le endpoint
de téléchargement sert directement ce fichier.

## Performances

Le workflow est optimisé pour que les builds **après le premier** soient rapides :

- **App sans dépendance** : `Activity` framework pure (pas d'AppCompat/AndroidX)
  → rien à télécharger ni à compiler côté bibliothèques.
- **Caches Gradle persistés** entre runs par `gradle/actions/setup-gradle`
  (dépendances + build cache + configuration cache).
- **Compilation Kotlin réutilisée** : le code ne change pas d'un build à l'autre
  (seules les ressources — URL, nom, package — changent), donc sa sortie est mise
  en cache et n'est pas recompilée.

En pratique : 1er build ~2-3 min (remplissage des caches), builds suivants ~1 min.

## Limites & pistes d'évolution (MVP)

- **APK debug** signé avec la clé debug Android → installable, mais pas
  publiable sur le Play Store. Ajouter un keystore + `signingConfigs` release
  (secrets GitHub) pour un APK/AAB signé.
- **Icône** : icône système par défaut. Passer une `icon_url` en input et
  générer les mipmaps dans le workflow pour une icône personnalisée.
- **Store en mémoire** : les builds sont perdus au redémarrage. Remplacer
  `internal/buildstore` par Redis/Postgres pour la persistance.
- **Webhook** au lieu du polling : faire notifier le serveur par le workflow
  (`repository_dispatch` ou endpoint HTTP) en fin de build.
- **Sécurité** : ajouter une authentification sur l'API avant toute exposition
  publique (le serveur déclenche des runs GitHub Actions).

## Structure

```
cmd/server/            point d'entrée
internal/config/       chargement de la config (env)
internal/ghclient/     appels API GitHub (dispatch, runs, artifacts)
internal/buildstore/   store des builds (en mémoire)
internal/server/       API HTTP + orchestration du suivi
.github/workflows/     build-apk.yml
android-template/       projet Android WebView (template)
```
