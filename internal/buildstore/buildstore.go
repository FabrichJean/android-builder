// Package buildstore conserve l'état des builds en cours et terminés.
// Implémentation en mémoire, protégée par un mutex (suffisant pour un MVP ;
// remplaçable par Redis/DB via la même interface).
package buildstore

import (
	"sync"
	"time"
)

// Status décrit l'étape courante d'un build.
type Status string

const (
	StatusPending  Status = "pending"  // dispatch envoyé, run pas encore repéré
	StatusBuilding Status = "building" // run en cours sur GitHub Actions
	StatusSuccess  Status = "success"  // APK disponible
	StatusFailed   Status = "failed"   // échec (voir Error)
)

// Build représente une demande de construction d'APK.
type Build struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	AppName   string    `json:"app_name"`
	Package   string    `json:"package"`
	Status    Status    `json:"status"`
	Error     string    `json:"error,omitempty"`
	RunID     int64     `json:"run_id,omitempty"`
	RunURL    string    `json:"run_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	apk []byte // contenu de l'APK une fois le build réussi (non sérialisé)
}

// Store est un dépôt thread-safe de builds.
type Store struct {
	mu     sync.RWMutex
	builds map[string]*Build
}

func New() *Store {
	return &Store{builds: make(map[string]*Build)}
}

// Create enregistre un nouveau build.
func (s *Store) Create(b *Build) {
	b.CreatedAt = time.Now()
	b.UpdatedAt = b.CreatedAt
	s.mu.Lock()
	s.builds[b.ID] = b
	s.mu.Unlock()
}

// Get renvoie une copie superficielle du build (sans l'APK).
func (s *Store) Get(id string) (*Build, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.builds[id]
	if !ok {
		return nil, false
	}
	cp := *b
	cp.apk = nil
	return &cp, true
}

// APK renvoie le contenu de l'APK d'un build réussi.
func (s *Store) APK(id string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.builds[id]
	if !ok || b.apk == nil {
		return nil, false
	}
	return b.apk, true
}

// Update applique une mutation au build sous verrou.
func (s *Store) Update(id string, fn func(*Build)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.builds[id]; ok {
		fn(b)
		b.UpdatedAt = time.Now()
	}
}

// SetAPK stocke l'APK et passe le build en succès.
func (s *Store) SetAPK(id string, apk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.builds[id]; ok {
		b.apk = apk
		b.Status = StatusSuccess
		b.UpdatedAt = time.Now()
	}
}
