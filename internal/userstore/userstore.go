// Package userstore mémorise les comptes Google vus à la connexion (id "sub",
// email, nom, photo). Les crédits et budgets de génération sont suivis par
// user_id : cette table sert à remettre un email et un nom sur ces ids,
// notamment dans l'espace admin.
package userstore

import (
	"database/sql"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id         TEXT PRIMARY KEY,
	email      TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	picture    TEXT NOT NULL DEFAULT '',
	last_login INTEGER NOT NULL DEFAULT 0
);
`

// User est un compte Google vu au moins une fois à la connexion.
type User struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Picture   string `json:"picture"`
	LastLogin int64  `json:"last_login"` // Unix ms
}

// Store persiste les comptes dans la base SQLite partagée du serveur.
// db nil = store désactivé (toutes les méthodes deviennent des no-op).
type Store struct{ db *sql.DB }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return &Store{}, nil
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Upsert enregistre (ou met à jour) un compte au moment de la connexion.
func (s *Store) Upsert(id, email, name, picture string) error {
	if s.db == nil || id == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO users (id, email, name, picture, last_login) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			email = excluded.email,
			name = excluded.name,
			picture = excluded.picture,
			last_login = excluded.last_login
	`, id, email, name, picture, time.Now().UnixMilli())
	return err
}

// List renvoie tous les comptes connus, dernière connexion en premier.
func (s *Store) List() ([]*User, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id, email, name, picture, last_login FROM users ORDER BY last_login DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Picture, &u.LastLogin); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
