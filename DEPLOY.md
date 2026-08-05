# Déploiement en production

Ce document couvre la mise en ligne du serveur (`cmd/server`) sur une machine
(VPS, serveur dédié…), au-delà du `go run` local décrit dans le [README](README.md).
Pour le détail des variables d'environnement et de l'API, se référer au README —
ce guide se concentre sur l'exploitation : installation, persistance, reverse
proxy/HTTPS, mise à jour, sauvegardes, sécurité.

## 1. Vue d'ensemble de ce qu'il faut prévoir

Le serveur est un seul binaire Go **sans dépendance runtime obligatoire**, mais
deux fonctionnalités optionnelles dépendent d'outils présents sur la machine :

| Fonctionnalité                          | Dépendance système          | Si absente                          |
|------------------------------------------|------------------------------|--------------------------------------|
| Miniatures des builds (screenshots)       | Chrome/Chromium (`CHROME_PATH`) | Désactivées, repli sur dégradé/mShots |
| Génération d'app par IA                   | CLI `claude` dans le `PATH` (`CLAUDE_BIN`) | Désactivée, reste du site inchangé |

Le serveur détecte leur présence au démarrage et log un avertissement si elles
manquent — ce n'est jamais bloquant.

Trois éléments constituent l'**état persistant** de l'application (à sauvegarder,
et à ne jamais perdre lors d'une mise à jour) :

```
data.db      base SQLite : builds, historique par compte, abus IA
releases/    APK produits + sources (dist.zip) des builds "bundle"
thumbs/      miniatures PNG générées
```

Ces trois chemins sont déjà exclus du dépôt Git (`.gitignore`) — un `git pull`
ne les touche jamais.

## 2. Prérequis

1. Un **dépôt GitHub** contenant ce projet (workflow `.github/workflows/build-apk.yml`
   + `android-template/`) — c'est lui qui compile réellement les APK via Actions.
2. Un **token GitHub** (PAT classique ou fine-grained) avec la permission
   **Actions: read and write** sur ce dépôt.
3. Sur le serveur : soit **Go 1.26+** pour compiler le binaire, soit **Docker**
   (voir limites du Dockerfile fourni, §6).
4. (Optionnel) **Chrome/Chromium** installé si tu veux les miniatures de builds.
5. (Optionnel) le **CLI `claude`** installé et authentifié si tu veux activer la
   génération d'app par IA (`internal/appgen`) — c'est un abonnement/accès distinct
   d'Anthropic, à configurer séparément sur la machine.

## 3. Récupérer et compiler

```bash
git clone <url-de-ton-fork-ou-repo> android-builder
cd android-builder
go build -o /opt/android-builder/server ./cmd/server
```

Place les fichiers d'exécution dans un dossier dédié, par exemple `/opt/android-builder/` :

```
/opt/android-builder/
├── server            # binaire compilé
├── .env              # configuration (jamais commité)
├── data.db           # créé automatiquement au premier lancement
├── releases/
└── thumbs/
```

## 4. Configuration (`.env`)

Copie `.env.example` vers `/opt/android-builder/.env` et remplis-le — voir le
[README](README.md#configuration) pour le détail de chaque variable. Points
spécifiques à un déploiement en production :

- **`BASE_URL`** : mets la **vraie URL publique en https** (ex.
  `https://apk.mondomaine.com`), même si le serveur écoute en HTTP en interne
  derrière un reverse proxy (§5). Cette valeur détermine :
  - l'URI de redirection OAuth Google attendue (`BASE_URL/auth/google/callback`) ;
  - le flag `Secure` des cookies de session (posé dès que `BASE_URL` commence par
    `https://`) — si tu sers réellement en HTTPS mais laisses `BASE_URL` en
    `http://`, les cookies partent sans `Secure` (pas grave en soi, mais moins
    strict que nécessaire) ; si tu mets `https://` sans que le trafic soit
    réellement chiffré de bout en bout, les cookies `Secure` ne seront pas
    renvoyés par le navigateur et la connexion Google semblera « ne pas tenir ».
- **`SESSION_SECRET`** : **fixe une valeur toi-même** en production. Laissée
  vide, elle est régénérée aléatoirement à chaque démarrage — tous les comptes
  connectés seraient déconnectés à chaque redémarrage/déploiement.
- **`PORT`** : le serveur écoute en clair sur ce port ; en pratique il ne doit
  être exposé qu'au reverse proxy (voir §5), pas directement à Internet.
- **`RELEASES_DIR` / `THUMBS_DIR` / `DB_PATH`** : laisse les valeurs par défaut
  si tu lances le binaire depuis `/opt/android-builder/` (chemins relatifs au
  répertoire de travail), ou mets des chemins absolus si tu préfères séparer
  code et données (utile pour les sauvegardes, §7).

## 5. Lancer en service (systemd)

Exemple d'unité `/etc/systemd/system/android-builder.service` :

```ini
[Unit]
Description=android-builder
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/android-builder
EnvironmentFile=/opt/android-builder/.env
ExecStart=/opt/android-builder/server
Restart=on-failure
RestartSec=5
User=android-builder
# Durcissement optionnel :
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home /opt/android-builder --shell /usr/sbin/nologin android-builder
sudo chown -R android-builder:android-builder /opt/android-builder
sudo systemctl daemon-reload
sudo systemctl enable --now android-builder
sudo journalctl -u android-builder -f   # logs JSON structurés (slog), en direct
```

Le serveur expose `GET /healthz` (`{"status":"ok"}`) — utilisable par un
`ExecStartPost`/watchdog ou par ton load-balancer pour un contrôle de santé.

## 6. Reverse proxy + HTTPS

Le serveur ne fait pas de TLS lui-même : mets-le derrière **nginx** ou **Caddy**
sur la même machine, qui écoute en 443 et relaie en HTTP vers `127.0.0.1:$PORT`.

**Caddy** (le plus simple — HTTPS automatique via Let's Encrypt) :

```
apk.mondomaine.com {
    reverse_proxy 127.0.0.1:8080
}
```

**nginx** :

```nginx
server {
    listen 443 ssl http2;
    server_name apk.mondomaine.com;

    ssl_certificate     /etc/letsencrypt/live/apk.mondomaine.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/apk.mondomaine.com/privkey.pem;

    # SSE (/api/builds/{id}/events) : désactive le buffering pour un flux temps réel
    proxy_buffering off;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

Le endpoint SSE (`/events`) doit impérativement passer sans buffering/compression
côté proxy, sinon la progression en temps réel de l'interface reste bloquée
jusqu'à la fin du build.

## 7. Docker (option alternative)

Le `Dockerfile` fourni compile un binaire statique dans une image
**distroless** minimale — pratique et sûr, mais avec deux limites à connaître :

- **Pas de Chrome** dans l'image → les miniatures de builds seront désactivées
  (repli automatique, rien de cassé, juste moins de captures d'écran).
- **Pas de CLI `claude`** → génération d'app par IA désactivée.

Si ces deux fonctionnalités t'importent, construis une image dérivée qui les
installe (base Debian/Ubuntu + `chromium` + le binaire `claude`), plutôt que
l'image distroless telle quelle.

Avec `docker-compose.yml`, en montant les trois chemins persistants en volumes :

```yaml
services:
  android-builder:
    build: .
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"   # derrière un reverse proxy sur l'hôte
    env_file: .env
    volumes:
      - ./data.db:/data.db
      - ./releases:/releases
      - ./thumbs:/thumbs
    environment:
      DB_PATH: /data.db
      RELEASES_DIR: /releases
      THUMBS_DIR: /thumbs
```

(L'image distroless n'a pas de shell : `docker exec -it ... sh` ne fonctionnera
pas pour du diagnostic — passe par les logs `docker logs`.)

## 8. Mise à jour

```bash
cd /opt/android-builder-src   # ton clone git (séparé du dossier d'exécution)
git pull
go build -o /opt/android-builder/server.new ./cmd/server
sudo systemctl stop android-builder
mv /opt/android-builder/server.new /opt/android-builder/server
sudo systemctl start android-builder
```

Les migrations de schéma SQLite (ex. nouvelles colonnes) sont appliquées
automatiquement au démarrage (`ALTER TABLE ... ADD COLUMN`, erreurs "colonne
existe déjà" ignorées) — aucune étape manuelle nécessaire. `data.db` n'est
jamais touché par un `git pull` (exclu du dépôt).

## 9. Sauvegardes

À sauvegarder régulièrement (arrêter le service ou utiliser `sqlite3 .backup`
pour `data.db` afin d'éviter une copie à chaud incohérente) :

```bash
sqlite3 /opt/android-builder/data.db ".backup '/backup/data-$(date +%F).db'"
rsync -a /opt/android-builder/releases/ /backup/releases/
```

`thumbs/` est régénérable (juste des captures d'écran) — moins critique à
sauvegarder que `data.db` (métadonnées + historique) et `releases/sources/`
(seul endroit où le code source des projets "bundle" persiste au-delà de la
release GitHub temporaire, supprimée après chaque build).

## 10. Sécurité — à lire avant une exposition publique large

Comme noté dans le README, la connexion Google protège l'**historique** (privé
par compte), mais **pas** le déclenchement de build lui-même : quiconque connaît
l'URL du serveur peut lancer un build (consommant tes minutes GitHub Actions).
Avant une exposition publique large, ajoute au minimum l'un de :

- une authentification en amont (Basic Auth au niveau du reverse proxy, VPN,
  liste blanche d'IP) ;
- un rate-limiting par IP sur `POST /api/builds` (ex. `limit_req` nginx) ;
- une clé d'API/jeton partagé exigé dans les requêtes de création de build.

Autres points d'attention :

- Le dossier `releases/` grossit indéfiniment (un fichier par build réussi) —
  prévoir une rotation/purge (ex. cron supprimant les APK de plus de N jours),
  sachant que la suppression d'un build depuis l'interface (soft delete) ne
  supprime pas encore les fichiers sur disque.
- `GEN_TOKEN_LIMIT` (défaut 10000) borne le coût de la génération IA par session
  — à ajuster selon ton budget si la fonctionnalité est activée.
- `ADMIN_EMAILS` (liste d'emails Google séparés par des virgules) ouvre
  l'espace admin `/admin` : budget de tokens journalier par utilisateur et
  remise à zéro des crédits IA (un compte ou tous). Vide = pas d'admin.
