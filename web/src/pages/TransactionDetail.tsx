import { useState } from "react";
import { useParams, useNavigate, useLocation } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, patch, fmtCents, ApiError } from "../api/client";
import { useAddCategoryFromNote, useIgnoreNote } from "../api/categories";
import type { Category, Txn } from "../api/types";
import { ChevronLeftIcon } from "../components/Icons";

export default function TransactionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<string | null>(null);
  // Off by default: labelling one transaction shouldn't quietly rewrite notes
  // on past ones. Opting in is a deliberate choice.
  const [applyToMerchant, setApplyToMerchant] = useState(false);
  const [error, setError] = useState("");

  // Opening this page from a push notification is a fresh history entry with
  // nothing to pop, so fall back to the transaction list.
  const goBack = () => {
    if (location.key === "default") navigate("/transactions");
    else navigate(-1);
  };

  const txn = useQuery({
    queryKey: ["transaction", id],
    queryFn: () => get<Txn>(`/api/transactions/${id}`),
    enabled: !!id,
  });
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });
  const addFromNote = useAddCategoryFromNote();
  const ignoreNote = useIgnoreNote();

  const save = useMutation({
    mutationFn: (categoryID: string) =>
      patch(`/api/transactions/${id}/category`, {
        category_id: categoryID,
        apply_to_merchant: applyToMerchant,
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["transactions"] });
      await qc.invalidateQueries({ queryKey: ["transaction", id] });
      // Cash flow totals shift when a category changes.
      await qc.invalidateQueries({ queryKey: ["cashflow"] });
      goBack();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not save"),
  });

  if (txn.isLoading) {
    return (
      <div className="center">
        <div className="spinner" />
      </div>
    );
  }
  if (txn.isError || !txn.data) {
    return (
      <div className="center">
        <p className="muted">Transaction not found.</p>
      </div>
    );
  }
  const t = txn.data;
  const currentCategory = selected ?? t.category_id;

  return (
    <>
      <button className="back-link" onClick={goBack}>
        <ChevronLeftIcon size={17} />
        Back
      </button>
      <h1>Transaction</h1>
      <div className="card" style={{ textAlign: "center" }}>
        <div className="txn-logo" style={{ margin: "0 auto 10px", width: 56, height: 56 }}>
          {t.image_url ? <img src={t.image_url} alt="" /> : (t.payee || "?").charAt(0)}
        </div>
        <div style={{ fontWeight: 700, fontSize: "1.1rem" }}>{t.payee || "Transaction"}</div>
        <div className={`balance-big ${t.amount_cents > 0 ? "pos" : ""}`}>
          {fmtCents(t.amount_cents, true)}
        </div>
        <div className="muted small">
          {new Date(t.occurred_at).toLocaleString()} {t.pending && "· pending"}
        </div>
        {t.subaccount_name && <div className="muted small">Pocket: {t.subaccount_name}</div>}
        <div style={{ marginTop: 8 }}>
          {t.category_name ? (
            <span className="pill accent">{t.category_name}</span>
          ) : (
            <span className="pill">Misc</span>
          )}
        </div>
        {t.has_user_note && (
          <p className="muted small" style={{ marginTop: 8 }}>
            Note in Crew: “{t.note}”
          </p>
        )}
      </div>

      {t.can_add_category && (
        <div className="card" style={{ background: "var(--surface2)" }}>
          <p style={{ fontWeight: 600, marginBottom: 4 }}>“{t.note}” isn’t a category yet</p>
          <p className="muted small" style={{ marginBottom: 10 }}>
            Add it to reuse it across the family, or ignore it if it’s a personal note.
          </p>
          <div className="row" style={{ gap: 8 }}>
            <button
              className="btn-small"
              style={{ width: "auto" }}
              onClick={() => addFromNote.mutate({ name: t.note })}
              disabled={addFromNote.isPending}
            >
              Add “{t.note}” as a category
            </button>
            <button
              className="btn-small btn-secondary"
              style={{ width: "auto" }}
              onClick={() => ignoreNote.mutate(t.note)}
              disabled={ignoreNote.isPending}
            >
              Ignore
            </button>
          </div>
        </div>
      )}

      <h2>{t.category_name ? "Change category" : "Pick a category"}</h2>
      <div className="category-grid">
        {(categories.data?.categories ?? []).map((c) => (
          <button
            key={c.id}
            className={`category-option ${currentCategory === c.id ? "selected" : ""}`}
            // A just-added category has a placeholder id until the server
            // responds; selecting it would send an unknown id.
            disabled={c.id.startsWith("pending-")}
            onClick={() => setSelected(c.id)}
          >
            {c.name}
          </button>
        ))}
      </div>
      {categories.data && categories.data.categories.length === 0 && (
        <p className="muted">
          No categories yet — create some in the Categories tab first.
        </p>
      )}

      {t.payee && (
        <label className="row" style={{ margin: "14px 0", gap: 10 }}>
          <input
            type="checkbox"
            style={{ width: "auto", margin: 0 }}
            checked={applyToMerchant}
            onChange={(e) => setApplyToMerchant(e.target.checked)}
          />
          <span className="small">
            Also label past <b>{t.payee}</b> transactions that have no note
          </span>
        </label>
      )}
      <p className="muted small" style={{ marginBottom: 18 }}>
        Saving writes the category to this transaction’s note in Crew, so it appears in the Crew
        app as well.
      </p>

      {error && <div className="error">{error}</div>}
      <button
        disabled={!selected || save.isPending}
        onClick={() => selected && save.mutate(selected)}
      >
        {save.isPending ? "Saving…" : "Save category"}
      </button>
    </>
  );
}
