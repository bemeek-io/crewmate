import { FormEvent, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, patch, del, ApiError } from "../api/client";
import type { Category, MerchantRule } from "../api/types";

const EMOJI_SUGGESTIONS = ["🛒", "🍔", "⛽", "🏠", "🎬", "👕", "💊", "🎁", "✈️", "📱", "🐾", "🎓"];

export default function Categories() {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [emoji, setEmoji] = useState("");
  const [error, setError] = useState("");

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });
  const rules = useQuery({
    queryKey: ["merchant-rules"],
    queryFn: () => get<{ rules: MerchantRule[] }>("/api/merchant-rules"),
  });

  const invalidate = async () => {
    await qc.invalidateQueries({ queryKey: ["categories"] });
    await qc.invalidateQueries({ queryKey: ["merchant-rules"] });
    await qc.invalidateQueries({ queryKey: ["transactions"] });
  };

  const create = useMutation({
    mutationFn: () => post("/api/categories", { name: name.trim(), emoji, color: "" }),
    onSuccess: async () => {
      setName("");
      setEmoji("");
      setError("");
      await invalidate();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not create"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => del(`/api/categories/${id}`),
    onSuccess: invalidate,
  });

  const updateRule = useMutation({
    mutationFn: ({ id, categoryID }: { id: string; categoryID: string }) =>
      patch(`/api/merchant-rules/${id}`, { category_id: categoryID }),
    onSuccess: invalidate,
  });
  const deleteRule = useMutation({
    mutationFn: (id: string) => del(`/api/merchant-rules/${id}`),
    onSuccess: invalidate,
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (name.trim()) create.mutate();
  }

  return (
    <>
      <h1>Categories</h1>
      <div className="card">
        <form onSubmit={submit}>
          <div className="row">
            <input
              placeholder="New category (e.g. Groceries)"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={40}
              style={{ marginBottom: 0 }}
            />
            <button className="btn-small" style={{ width: "auto" }} disabled={create.isPending}>
              Add
            </button>
          </div>
          <div className="row" style={{ marginTop: 8, flexWrap: "wrap", gap: 6 }}>
            {EMOJI_SUGGESTIONS.map((e2) => (
              <button
                type="button"
                key={e2}
                className="btn-small btn-secondary"
                style={{ width: "auto", opacity: emoji === e2 ? 1 : 0.6 }}
                onClick={() => setEmoji(emoji === e2 ? "" : e2)}
              >
                {e2}
              </button>
            ))}
          </div>
          {error && <div className="error">{error}</div>}
        </form>
      </div>

      <div className="card">
        {(categories.data?.categories ?? []).map((c) => (
          <div className="row spread" style={{ padding: "10px 0" }} key={c.id}>
            <span style={{ fontWeight: 600 }}>
              {c.emoji ? `${c.emoji} ` : ""}
              {c.name}
            </span>
            <button
              className="btn-small btn-danger"
              style={{ width: "auto" }}
              onClick={() => {
                if (confirm(`Delete "${c.name}"? Transactions keep their history uncategorized.`))
                  remove.mutate(c.id);
              }}
            >
              Delete
            </button>
          </div>
        ))}
        {categories.data && categories.data.categories.length === 0 && (
          <p className="muted">
            No categories yet. Add a few — new transactions are matched against them automatically.
          </p>
        )}
      </div>

      <h2>Merchant rules</h2>
      <p className="muted small" style={{ marginBottom: 10 }}>
        Auto-applied when a merchant matches. Rules marked “auto” were suggested by AI — correct
        them and they become yours.
      </p>
      <div className="card">
        {(rules.data?.rules ?? []).map((r) => (
          <div className="row spread" style={{ padding: "8px 0" }} key={r.id}>
            <div className="grow">
              <div className="txn-title">{r.merchant_key}</div>
              <span className={`pill ${r.source === "llm" ? "" : "accent"}`}>
                {r.source === "llm" ? "auto" : "yours"}
              </span>
            </div>
            <select
              style={{ width: "auto", marginBottom: 0 }}
              value={r.category_id}
              onChange={(e) => updateRule.mutate({ id: r.id, categoryID: e.target.value })}
            >
              {(categories.data?.categories ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <button
              className="btn-small btn-secondary"
              style={{ width: "auto" }}
              onClick={() => deleteRule.mutate(r.id)}
            >
              ✕
            </button>
          </div>
        ))}
        {rules.data && rules.data.rules.length === 0 && (
          <p className="muted">No rules yet — they appear as transactions get categorized.</p>
        )}
      </div>
    </>
  );
}
