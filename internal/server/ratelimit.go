package server

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// localLimiter encadre les builds par le serveur (CPU/RAM locaux) :
//   - quotas par compte, sur fenêtre glissante (heure et jour), persistés en
//     SQLite pour survivre aux redémarrages ;
//   - un seul build en cours (ou en attente) par compte ;
//   - une file d'attente globale plafonnée (les builds sont exécutés un par un).
type localLimiter struct {
	db       *sql.DB
	perHour  int
	perDay   int
	maxQueue int

	mu     sync.Mutex
	active map[string]bool // comptes ayant un build local en cours/en attente
	queued int
}

// limitError est renvoyée quand une demande est refusée (HTTP 429).
type limitError struct {
	msg        string
	retryAfter time.Duration // 0 = inconnu
}

func (e *limitError) Error() string { return e.msg }

func newLocalLimiter(db *sql.DB, perHour, perDay, maxQueue int) *localLimiter {
	l := &localLimiter{db: db, perHour: perHour, perDay: perDay, maxQueue: maxQueue, active: map[string]bool{}}
	if db != nil {
		db.Exec(`CREATE TABLE IF NOT EXISTS local_build_log (
			user_id    TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_local_build_log ON local_build_log(user_id, created_at)`)
	}
	return l
}

// Acquire réserve une place pour un build local de ce compte. exempt (admin)
// ignore les quotas mais pas la file d'attente. Le release renvoyé doit être
// appelé à la fin du build, ou si le build n'est finalement pas lancé.
func (l *localLimiter) Acquire(userID string, exempt bool) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.active[userID] {
		return nil, &limitError{msg: "un build par le serveur est déjà en cours pour ton compte, attends sa fin"}
	}
	if l.queued >= l.maxQueue {
		return nil, &limitError{msg: "la file d'attente des builds serveur est pleine, réessaie dans quelques minutes", retryAfter: time.Minute}
	}

	now := time.Now()
	if l.db != nil {
		l.db.Exec(`DELETE FROM local_build_log WHERE created_at < ?`, now.Add(-48*time.Hour).UnixMilli())
		if !exempt {
			for _, w := range []struct {
				d     time.Duration
				limit int
				label string
			}{
				{time.Hour, l.perHour, "heure"},
				{24 * time.Hour, l.perDay, "jour"},
			} {
				if w.limit <= 0 {
					continue
				}
				var n int
				var oldest int64
				l.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(created_at),0) FROM local_build_log
					WHERE user_id = ? AND created_at > ?`, userID, now.Add(-w.d).UnixMilli()).Scan(&n, &oldest)
				if n >= w.limit {
					wait := time.UnixMilli(oldest).Add(w.d).Sub(now)
					if wait < time.Second {
						wait = time.Second
					}
					return nil, &limitError{
						msg:        fmt.Sprintf("limite atteinte : %d builds par %s. Réessaie dans %s", w.limit, w.label, humanDuration(wait)),
						retryAfter: wait,
					}
				}
			}
		}
		l.db.Exec(`INSERT INTO local_build_log (user_id, created_at) VALUES (?, ?)`, userID, now.UnixMilli())
	}

	l.active[userID] = true
	l.queued++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.active, userID)
			l.queued--
			l.mu.Unlock()
		})
	}, nil
}

// Full indique si la file d'attente globale est saturée.
func (l *localLimiter) Full() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queued >= l.maxQueue
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "moins d'une minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
}
