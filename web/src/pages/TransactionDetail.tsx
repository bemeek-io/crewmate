import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, patch, fmtCents, ApiError } from "../api/client";
import type { Category, Txn } from "../api/types";

export default function TransactionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<string | null>(null);
  const [applyToMerchant, setApplyToMerchant] = useState(true);
  const [error, setError] = useState("");

  const txn = useQuery({
    queryKey: ["transaction", id],
    queryFn: () => get<Txn>(`/api/transactions/${id}`),
    enabled: !!id,
  });
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });

  const save = useMutation({
    mutationFn: (categoryID: string) =>
      patch(`/api/transactions/${id}/category`, {
        category_id: categoryID,
        apply_to_merchant: applyToMerchant,
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["transactions"] });
      await qc.invalidateQueries({ queryKey: ["transaction", id] });
      navigate(-1);
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
            <span className="pill warn">uncategorized</span>
          )}
        </div>
        {t.has_user_note && (
          <p className="muted small" style={{ marginTop: 8 }}>
            Note in Crew: “{t.note}” — choosing a category replaces it.
          </p>
        )}
      </div>

      <h2>{t.category_name ? "Change category" : "Pick a category"}</h2>
      <div className="category-grid">
        {(categories.data?.categories ?? []).map((c) => (
          <button
            key={c.id}
            className={`category-option ${currentCategory === c.id ? "selected" : ""}`}
            onClick={() => setSelected(c.id)}
          >
            {c.emoji ? `${c.emoji} ` : ""}
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
      <p className="muted small">
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
