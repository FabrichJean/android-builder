// Package buildstore conserve l'état des builds en cours et terminés, dans un
// fichier SQLite (survit aux redémarrages). UserID (vide pour les builds
// anonymes) permet un historique privé par compte Google connecté.
package buildstore

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Status décrit l'étape courante d'un build.
type Status string

const (
	StatusPending  Status = "pending"  // dispatch envoyé, run pas encore repéré
	StatusBuilding Status = "building" // run en cours sur GitHub Actions
	StatusSuccess  Status = "success"  // APK disponible
	StatusFailed   Status = "failed"   // échec (voir Error)
)

// Step est une étape du build telle que remontée par GitHub Actions.
type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, skipped, ...
}

// Build représente une demande de construction d'APK.
type Build struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id,omitempty"` // vide = build anonyme (pas d'historique serveur)
	URL         string    `json:"url"`
	AppName     string    `json:"app_name"`
	Package     string    `json:"package"`
	Mode        string    `json:"mode,omitempty"`      // "url" ou "bundle"
	SplashBg    string    `json:"splash_bg,omitempty"` // couleur de fond du splash
	HasIcon     bool      `json:"has_icon,omitempty"`  // icône personnalisée fournie
	Status      Status    `json:"status"`
	Error       string    `json:"error,omitempty"`
	RunID       int64     `json:"run_id,omitempty"`
	RunURL      string    `json:"run_url,omitempty"`
	APKPath     string    `json:"apk_path,omitempty"`    // chemin du fichier APK sur disque
	SourcePath  string    `json:"source_path,omitempty"` // chemin du dist.zip source sur disque (mode bundle)
	Steps       []Step    `json:"steps,omitempty"`    // étapes du build
	Progress    int       `json:"progress"`           // 0-100
	CurrentStep string    `json:"current_step,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Deleted     bool      `json:"-"` // suppression douce (bouton "Supprimer") : jamais exposé côté client
}

const schema = `
CREATE TABLE IF NOT EXISTS builds (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL DEFAULT '',
	url           TEXT NOT NULL DEFAULT '',
	app_name      TEXT NOT NULL DEFAULT '',
	package       TEXT NOT NULL DEFAULT '',
	mode          TEXT NOT NULL DEFAULT '',
	splash_bg     TEXT NOT NULL DEFAULT '',
	has_icon      INTEGER NOT NULL DEFAULT 0,
	status        TEXT NOT NULL DEFAULT '',
	error         TEXT NOT NULL DEFAULT '',
	run_id        INTEGER NOT NULL DEFAULT 0,
	run_url       TEXT NOT NULL DEFAULT '',
	apk_path      TEXT NOT NULL DEFAULT '',
	source_path   TEXT NOT NULL DEFAULT '',
	steps_json    TEXT NOT NULL DEFAULT '[]',
	progress      INTEGER NOT NULL DEFAULT 0,
	current_step  TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL DEFAULT 0,
	updated_at    INTEGER NOT NULL DEFAULT 0,
	deleted       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_builds_user ON builds(user_id, created_at DESC);
`

// Store est un dépôt de builds persisté en SQLite.
// Un mutex protège les cycles lecture-modification-écriture (Update) : le
// pilote SQLite sérialise déjà les écritures, mais on veut aussi que
// l'application de fn() soit atomique du point de vue de l'appelant.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// Open ouvre (ou crée) la base SQLite au chemin donné.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite : une seule connexion évite les "database is locked"
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Migration légère pour les bases créées avant l'ajout de source_path :
	// erreur "duplicate column" ignorée si la colonne existe déjà.
	db.Exec(`ALTER TABLE builds ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE builds ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0`)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB expose la connexion sous-jacente pour les autres composants qui ont
// besoin de leurs propres tables dans le même fichier SQLite (ex. le suivi
// des abus de génération d'app par IA) — évite d'ouvrir une seconde connexion
// concurrente vers le même fichier.
func (s *Store) DB() *sql.DB { return s.db }

// Create enregistre un nouveau build.
func (s *Store) Create(b *Build) error {
	now := time.Now()
	b.CreatedAt = now
	b.UpdatedAt = now
	return s.insert(b)
}

func (s *Store) insert(b *Build) error {
	stepsJSON, _ := json.Marshal(b.Steps)
	_, err := s.db.Exec(
		`INSERT INTO builds (id, user_id, url, app_name, package, mode, splash_bg, has_icon,
			status, error, run_id, run_url, apk_path, source_path, steps_json, progress, current_step,
			created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.UserID, b.URL, b.AppName, b.Package, b.Mode, b.SplashBg, boolToInt(b.HasIcon),
		string(b.Status), b.Error, b.RunID, b.RunURL, b.APKPath, b.SourcePath, string(stepsJSON), b.Progress, b.CurrentStep,
		b.CreatedAt.UnixMilli(), b.UpdatedAt.UnixMilli(),
	)
	return err
}

// Get renvoie une copie du build.
func (s *Store) Get(id string) (*Build, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.get(id)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (s *Store) get(id string) (*Build, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, url, app_name, package, mode, splash_bg, has_icon,
			status, error, run_id, run_url, apk_path, source_path, steps_json, progress, current_step,
			created_at, updated_at, deleted
		FROM builds WHERE id = ?`, id,
	)
	return scanBuild(row)
}

// List renvoie l'historique d'un utilisateur (le plus récent d'abord), builds
// supprimés (soft delete) exclus.
// userID vide -> tranche vide (les builds anonymes n'ont pas d'historique serveur).
func (s *Store) List(userID string) ([]*Build, error) {
	if userID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, url, app_name, package, mode, splash_bg, has_icon,
			status, error, run_id, run_url, apk_path, source_path, steps_json, progress, current_step,
			created_at, updated_at, deleted
		FROM builds WHERE user_id = ? AND deleted = 0 ORDER BY created_at DESC LIMIT 200`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Build{}
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// APKPath renvoie le chemin du fichier APK d'un build réussi.
func (s *Store) APKPath(id string) (string, bool) {
	b, ok := s.Get(id)
	if !ok || b.APKPath == "" {
		return "", false
	}
	return b.APKPath, true
}

// SourcePath renvoie le chemin du dist.zip source d'un build (mode bundle).
func (s *Store) SourcePath(id string) (string, bool) {
	b, ok := s.Get(id)
	if !ok || b.SourcePath == "" {
		return "", false
	}
	return b.SourcePath, true
}

// Update applique une mutation au build sous verrou (lecture -> fn -> écriture).
func (s *Store) Update(id string, fn func(*Build)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.get(id)
	if err != nil {
		return
	}
	fn(b)
	b.UpdatedAt = time.Now()
	stepsJSON, _ := json.Marshal(b.Steps)
	s.db.Exec(
		`UPDATE builds SET user_id=?, url=?, app_name=?, package=?, mode=?, splash_bg=?, has_icon=?,
			status=?, error=?, run_id=?, run_url=?, apk_path=?, source_path=?, steps_json=?, progress=?, current_step=?,
			updated_at=?, deleted=? WHERE id=?`,
		b.UserID, b.URL, b.AppName, b.Package, b.Mode, b.SplashBg, boolToInt(b.HasIcon),
		string(b.Status), b.Error, b.RunID, b.RunURL, b.APKPath, b.SourcePath, string(stepsJSON), b.Progress, b.CurrentStep,
		b.UpdatedAt.UnixMilli(), boolToInt(b.Deleted), id,
	)
}

// SetAPKPath enregistre le chemin de l'APK sur disque et passe le build en succès.
func (s *Store) SetAPKPath(id, path string) {
	s.Update(id, func(b *Build) {
		b.APKPath = path
		b.Status = StatusSuccess
	})
}

// SoftDelete marque un build comme supprimé : il disparaît de List() et des
// endpoints publics, mais la ligne (et les fichiers associés) reste en place
// sur le serveur — pas de suppression physique irréversible.
func (s *Store) SoftDelete(id string) {
	s.Update(id, func(b *Build) { b.Deleted = true })
}

// scanner abstrait *sql.Row et *sql.Rows (même méthode Scan).
type scanner interface {
	Scan(dest ...any) error
}

func scanBuild(row scanner) (*Build, error) {
	var b Build
	var hasIcon, deleted int
	var stepsJSON string
	var createdMs, updatedMs int64
	err := row.Scan(
		&b.ID, &b.UserID, &b.URL, &b.AppName, &b.Package, &b.Mode, &b.SplashBg, &hasIcon,
		&b.Status, &b.Error, &b.RunID, &b.RunURL, &b.APKPath, &b.SourcePath, &stepsJSON, &b.Progress, &b.CurrentStep,
		&createdMs, &updatedMs, &deleted,
	)
	if err != nil {
		return nil, err
	}
	b.HasIcon = hasIcon != 0
	b.Deleted = deleted != 0
	json.Unmarshal([]byte(stepsJSON), &b.Steps)
	b.CreatedAt = time.UnixMilli(createdMs)
	b.UpdatedAt = time.UnixMilli(updatedMs)
	return &b, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
