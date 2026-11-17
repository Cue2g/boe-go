package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sboe/internal/model"
)

func i64(v int64) *int64 { return &v }

func TestSync(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	t1 := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	batch := []model.Auction{
		{ExternalID: "a", URL: "u", ValuationCents: i64(10000), ClaimedCents: i64(2500)},
		{ExternalID: "b", URL: "u"},
	}
	n, err := s.Sync(ctx, "x", batch, t1)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}

	t2 := t1.Add(24 * time.Hour)
	n, err = s.Sync(ctx, "x", []model.Auction{batch[0], {ExternalID: "c", URL: "u"}}, t2)
	if err != nil || n != 1 {
		t.Fatalf("segunda: n=%d err=%v", n, err)
	}
	// vacío no desactiva nada
	if _, err := s.Sync(ctx, "x", nil, t2.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var total, active int
	s.db.QueryRow(`SELECT count(*), sum(active) FROM auctions`).Scan(&total, &active)
	if total != 3 || active != 2 {
		t.Errorf("total=%d active=%d", total, active)
	}
	var first, last string
	var pct float64
	s.db.QueryRow(`SELECT first_seen, last_seen, pct_claimed_valuation FROM auctions_v WHERE external_id='a'`).Scan(&first, &last, &pct)
	if first == last || pct != 25 {
		t.Errorf("first=%s last=%s pct=%v", first, last, pct)
	}

	// reabrir no re-aplica migraciones
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}
