package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// MonthlySpend's month boundaries are cut in SQL, in the family's zone, which
// the pure tests can't reach. Skipped unless CREWMATE_TEST_DATABASE_URL is set.
func TestMonthlySpendCutsMonthsInZone(t *testing.T) {
	url := os.Getenv("CREWMATE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set CREWMATE_TEST_DATABASE_URL to run store integration tests")
	}
	ctx := context.Background()
	st, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	suffix := uuid.NewString()
	var userID, familyID, connID, catID uuid.UUID
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(st.Pool.QueryRow(ctx, `INSERT INTO users (crew_user_id) VALUES ($1) RETURNING id`,
		"crew-"+suffix).Scan(&userID), "insert user")
	must(st.Pool.QueryRow(ctx, `INSERT INTO families (name) VALUES ($1) RETURNING id`,
		"fam-"+suffix).Scan(&familyID), "insert family")
	must(st.Pool.QueryRow(ctx,
		`INSERT INTO crew_connections (user_id, token_ciphertext) VALUES ($1,$2) RETURNING id`,
		userID, []byte("x")).Scan(&connID), "insert connection")
	must(st.Pool.QueryRow(ctx,
		`INSERT INTO categories (family_id, name, color) VALUES ($1,'Groceries','#22c55e') RETURNING id`,
		familyID).Scan(&catID), "insert category")

	txn := func(id string, cents int64, note string, at time.Time) {
		t.Helper()
		_, err := st.Pool.Exec(ctx, `
			INSERT INTO transactions
				(family_id, connection_id, crew_txn_id, amount_cents, payee, merchant_key, note, occurred_at)
			VALUES ($1,$2,$3,$4,'SHOP','shop',$5,$6)`,
			familyID, connID, id+suffix, cents, note, at)
		must(err, "insert txn "+id)
	}
	// 9pm on Aug 31 in Denver is already September in UTC.
	txn("late-aug", -40_00, "groceries", time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC))
	txn("sep", -10_00, "", time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC))
	txn("refund", 25_00, "Groceries", time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC))

	den, _ := time.LoadLocation("America/Denver")
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, den)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, den)
	rows, err := st.MonthlySpend(ctx, familyID, start, end, "America/Denver")
	must(err, "monthly spend")

	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2 (refunds aren't spending)", rows)
	}
	aug, sep := rows[0], rows[1]
	if aug.Month.Format("2006-01") != "2026-08" || aug.CategoryID == nil || *aug.CategoryID != catID || aug.Cents != 40_00 {
		t.Errorf("august row = %+v, want Groceries $40 (matched case-insensitively)", aug)
	}
	if sep.Month.Format("2006-01") != "2026-09" || sep.CategoryID != nil || sep.Cents != 10_00 {
		t.Errorf("september row = %+v, want uncategorized $10", sep)
	}
}
