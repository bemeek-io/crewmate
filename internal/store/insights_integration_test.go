package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The insights queries, dismissals and verdict cache against a real database.
// Skipped unless CREWMATE_TEST_DATABASE_URL is set.
func TestInsightsStore(t *testing.T) {
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
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	suffix := uuid.NewString()
	var userID, familyID, connID uuid.UUID
	must(st.Pool.QueryRow(ctx, `INSERT INTO users (crew_user_id) VALUES ($1) RETURNING id`,
		"crew-"+suffix).Scan(&userID), "insert user")
	must(st.Pool.QueryRow(ctx, `INSERT INTO families (name) VALUES ($1) RETURNING id`,
		"fam-"+suffix).Scan(&familyID), "insert family")
	must(st.Pool.QueryRow(ctx,
		`INSERT INTO crew_connections (user_id, token_ciphertext) VALUES ($1,$2) RETURNING id`,
		userID, []byte("x")).Scan(&connID), "insert connection")
	_, err = st.Pool.Exec(ctx, `INSERT INTO categories (family_id, name) VALUES ($1,'Dining')`, familyID)
	must(err, "insert category")

	txn := func(id, payee, mcc, note string, cents int64, at time.Time) {
		t.Helper()
		_, err := st.Pool.Exec(ctx, `
			INSERT INTO transactions
				(family_id, connection_id, crew_txn_id, amount_cents, payee, merchant_key, mcc, note, occurred_at)
			VALUES ($1,$2,$3,$4,$5,'doordash',$6,$7,$8)`,
			familyID, connID, id+suffix, cents, payee, mcc, note, at)
		must(err, "insert txn "+id)
	}
	txn("a", "DD *OLD NAME", "5812", "dining", -20_00, time.Date(2026, 7, 3, 18, 0, 0, 0, time.UTC))
	txn("b", "DoorDash", "5814", "Dining", -30_00, time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC))
	txn("c", "DoorDash", "5814", "", -15_00, time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC))
	txn("refund", "DoorDash", "5814", "Dining", 30_00, time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC))

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rows, err := st.SpendByMerchantMonth(ctx, familyID, start, end, "UTC")
	must(err, "spend by merchant month")
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want July Dining and August uncategorized", rows)
	}
	for _, r := range rows {
		switch r.Month.Format("2006-01") {
		case "2026-07":
			if r.CategoryName != "Dining" || r.Cents != 50_00 || r.Count != 2 {
				t.Errorf("july = %+v", r)
			}
		case "2026-08":
			if r.CategoryID != nil || r.Cents != 15_00 {
				t.Errorf("august = %+v (the refund isn't spending)", r)
			}
		default:
			t.Errorf("unexpected month %v", r.Month)
		}
	}

	profiles, err := st.MerchantProfiles(ctx, familyID, start, end)
	must(err, "profiles")
	if p := profiles["doordash"]; p.Payee != "DoorDash" || p.MCC != "5814" {
		t.Errorf("profile = %+v, want the latest name and code", p)
	}

	must(st.DismissInsight(ctx, familyID, userID, SubjectMerchant, "doordash", "DoorDash"), "dismiss")
	must(st.DismissInsight(ctx, familyID, userID, SubjectMerchant, "doordash", "DoorDash"), "dismiss twice")
	ds, err := st.ListInsightDismissals(ctx, familyID)
	must(err, "list dismissals")
	if len(ds) != 1 || ds[0].Label != "DoorDash" {
		t.Errorf("dismissals = %+v", ds)
	}
	must(st.RestoreInsight(ctx, familyID, SubjectMerchant, "doordash"), "restore")
	if err := st.RestoreInsight(ctx, familyID, SubjectMerchant, "doordash"); err != ErrNotFound {
		t.Errorf("second restore = %v, want ErrNotFound", err)
	}

	v := InsightVerdict{SubjectType: SubjectMerchant, SubjectKey: "doordash", Fingerprint: "f1",
		Verdict: "discretionary", Note: "Food delivery."}
	must(st.SaveInsightVerdicts(ctx, familyID, []InsightVerdict{v}), "save verdict")
	v.Fingerprint, v.Verdict = "f2", "unclear"
	must(st.SaveInsightVerdicts(ctx, familyID, []InsightVerdict{v}), "replace verdict")
	vs, err := st.InsightVerdicts(ctx, familyID)
	must(err, "verdicts")
	if got := vs["merchant:doordash"]; got.Fingerprint != "f2" || got.Verdict != "unclear" {
		t.Errorf("verdict = %+v, want the replacement", got)
	}
}
