package server

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func testLimiter(t *testing.T, perHour, perDay, queue int) *localLimiter {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return newLocalLimiter(db, perHour, perDay, queue)
}

func TestLimiterOneActivePerUser(t *testing.T) {
	l := testLimiter(t, 10, 10, 10)
	rel, err := l.Acquire("u1", false)
	if err != nil {
		t.Fatal(err)
	}
	var le *limitError
	if _, err := l.Acquire("u1", false); !errors.As(err, &le) {
		t.Fatalf("2e build simultané du même compte: attendu limitError, obtenu %v", err)
	}
	if _, err := l.Acquire("u2", false); err != nil {
		t.Fatalf("un autre compte doit passer: %v", err)
	}
	rel()
	rel() // idempotent
	if _, err := l.Acquire("u1", false); err != nil {
		t.Fatalf("après release, le compte doit pouvoir relancer: %v", err)
	}
}

func TestLimiterHourlyQuota(t *testing.T) {
	l := testLimiter(t, 2, 100, 10)
	for i := 0; i < 2; i++ {
		rel, err := l.Acquire("u1", false)
		if err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
		rel()
	}
	_, err := l.Acquire("u1", false)
	var le *limitError
	if !errors.As(err, &le) || le.retryAfter <= 0 {
		t.Fatalf("3e build: attendu limitError avec retryAfter, obtenu %v", err)
	}
	if _, err := l.Acquire("u2", false); err != nil {
		t.Fatalf("le quota est par compte: %v", err)
	}
}

func TestLimiterAdminExemptFromQuota(t *testing.T) {
	l := testLimiter(t, 1, 1, 10)
	for i := 0; i < 3; i++ {
		rel, err := l.Acquire("admin", true)
		if err != nil {
			t.Fatalf("admin build %d: %v", i, err)
		}
		rel()
	}
}

func TestLimiterQueueFull(t *testing.T) {
	l := testLimiter(t, 10, 10, 2)
	l.Acquire("a", false)
	l.Acquire("b", false)
	var le *limitError
	if _, err := l.Acquire("c", false); !errors.As(err, &le) {
		t.Fatalf("file pleine: attendu limitError, obtenu %v", err)
	}
	if _, err := l.Acquire("admin", true); !errors.As(err, &le) {
		t.Fatalf("la file plafonne aussi les admins, obtenu %v", err)
	}
}
