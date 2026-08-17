import { FormEvent, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, patch, del, fmtCents, ApiError } from "../api/client";
import type { Category, CategoryRule, Direction, MatchType } from "../api/types";
import { CloseIcon } from "../components/Icons";

interface Draft {
  category_id: string;
  payee_match: string;
  match_type: MatchType;
  min: string;
  max: string;
  direction: Direction;
  priority: string;
  apply_to_existing: boolean;
}

const emptyDraft = (categoryID = ""): Draft => ({
  category_id: categoryID,
  payee_match: "",
  match_type: "contains",
  min: "",
  max: "",
  direction: "spend",
  priority: "100",
  apply_to_existing: false,
});

/** Dollars in the form, cents on the wire. */
function toCents(v: string): number | null {
  const t = v.trim();
  if (!t) return null;
  const n = Number(t);
  return Number.isFinite(n) && n >= 0 ? Math.round(n * 100) : null;
}

function describe(r: CategoryRule): string {
  const parts: string[] = [];
  if (r.payee_match) {
    const verb =
      r.match_type === "equals" ? "is" : r.match_type === "prefix" ? "starts with" : "contains";
    parts.push(`vendor ${verb} “${r.payee_match}”`);
  }
  if (r.min_amount_cents != null && r.max_amount_cents != null) {
    parts.push(`between ${fmtCents(r.min_amount_cents)} and ${fmtCents(r.max_amount_cents)}`);
  } else if (r.min_amount_cents != null) {
    parts.push(`over ${fmtCents(r.min_amount_cents)}`);
  } else if (r.max_amount_cents != null) {
    parts.push(`under ${fmtCents(r.max_amount_cents)}`);
  }
  if (r.mcc) parts.push(`MCC ${r.mcc}`);
  if (r.direction !== "any") parts.push(r.direction === "spend" ? "spending" : "income");
  return parts.length ? parts.join(", ") : "every transaction";
}

export default function Rules() {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });
  const rules = useQuery({
    queryKey: ["rules"],
    queryFn: () => get<{ rules: CategoryRule[] }>("/api/rules"),
  });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["rules"] });
    qc.invalidateQueries({ queryKey: ["transactions"] });
  };

  const create = useMutation({
    mutationFn: (d: Draft) =>
      post("/api/rules", {
        category_id: d.category_id,
        payee_match: d.payee_match,
        match_type: d.match_type,
        min_amount_cents: toCents(d.min),
        max_amount_cents: toCents(d.max),
        direction: d.direction,
        priority: Number(d.priority) || 100,
        apply_to_existing: d.apply_to_existing,
      }),
    onSuccess: () => {
      setDraft(null);
      setError("");
      invalidate();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not save rule"),
  });

  const toggle = useMutation({
    mutationFn: (r: CategoryRule) =>
      patch(`/api/rules/${r.id}`, {
        category_id: r.category_id,
        payee_match: r.payee_match,
        match_type: r.match_type,
        mcc: r.mcc,
        min_amount_cents: r.min_amount_cents,
        max_amount_cents: r.max_amount_cents,
        direction: r.direction,
        priority: r.priority,
        enabled: !r.enabled,
      }),
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: (id: string) => del(`/api/rules/${id}`),
    onSuccess: invalidate,
  });

  const cats = categories.data?.categories ?? [];
  const list = rules.data?.rules ?? [];

  function submit(e: FormEvent) {
    e.preventDefault();
    if (draft?.category_id) create.mutate(draft);
  }

  return (
    <>
      <h1>Rules</h1>
      <p className="muted small" style={{ marginBottom: 14 }}>
        Rules run before the AI and never send a notification — a rule match is an outcome you
        already asked for. Anything a rule doesn’t catch still gets auto-categorized or flagged
        for you.
      </p>

      {draft === null ? (
        <button onClick={() => setDraft(emptyDraft(cats[0]?.id ?? ""))} disabled={cats.length === 0}>
          Add a rule
        </button>
      ) : (
        <div className="card">
          <form onSubmit={submit}>
            <label className="field-label">When a transaction…</label>
            <div className="row" style={{ gap: 8 }}>
              <select
                value={draft.match_type}
                onChange={(e) => setDraft({ ...draft, match_type: e.target.value as MatchType })}
                style={{ width: "auto", marginBottom: 0 }}
              >
                <option value="contains">vendor contains</option>
                <option value="equals">vendor is exactly</option>
                <option value="prefix">vendor starts with</option>
              </select>
              <input
                className="grow"
                placeholder="e.g. costco"
                value={draft.payee_match}
                onChange={(e) => setDraft({ ...draft, payee_match: e.target.value })}
                style={{ marginBottom: 0 }}
              />
            </div>

            <label className="field-label">…and the amount is</label>
            <div className="row" style={{ gap: 8 }}>
              <input
                inputMode="decimal"
                placeholder="min $"
                value={draft.min}
                onChange={(e) => setDraft({ ...draft, min: e.target.value })}
                style={{ marginBottom: 0 }}
              />
              <input
                inputMode="decimal"
                placeholder="max $"
                value={draft.max}
                onChange={(e) => setDraft({ ...draft, max: e.target.value })}
                style={{ marginBottom: 0 }}
              />
              <select
                value={draft.direction}
                onChange={(e) => setDraft({ ...draft, direction: e.target.value as Direction })}
                style={{ width: "auto", marginBottom: 0 }}
              >
                <option value="spend">spending</option>
                <option value="income">income</option>
                <option value="any">either</option>
              </select>
            </div>

            <label className="field-label">…then categorize it as</label>
            <select
              value={draft.category_id}
              onChange={(e) => setDraft({ ...draft, category_id: e.target.value })}
            >
              {cats.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>

            {/* Fills in transactions that were never categorized; it can't
                overwrite a category already chosen. */}
            <label className="row" style={{ margin: "14px 0", gap: 10 }}>
              <input
                type="checkbox"
                style={{ width: "auto", margin: 0 }}
                checked={draft.apply_to_existing}
                onChange={(e) => setDraft({ ...draft, apply_to_existing: e.target.checked })}
              />
              <span className="small">Also apply to past uncategorized transactions</span>
            </label>

            {error && <div className="error">{error}</div>}
            <div className="row" style={{ gap: 8 }}>
              <button disabled={create.isPending} style={{ width: "auto" }}>
                {create.isPending ? "Saving…" : "Save rule"}
              </button>
              <button
                type="button"
                className="btn-secondary"
                style={{ width: "auto" }}
                onClick={() => {
                  setDraft(null);
                  setError("");
                }}
              >
                Cancel
              </button>
            </div>
          </form>
        </div>
      )}

      {cats.length === 0 && (
        <p className="muted small">Create a category first — a rule has to assign one.</p>
      )}

      <h2>Your rules</h2>
      <div className="card">
        {list.map((r) => (
          <div className="row spread" style={{ padding: "12px 0", gap: 10 }} key={r.id}>
            <div className="grow" style={{ opacity: r.enabled ? 1 : 0.45 }}>
              <div className="row" style={{ gap: 8 }}>
                <span
                  className="swatch"
                  style={r.category_color ? { background: r.category_color } : undefined}
                />
                <span style={{ fontWeight: 600 }}>{r.category_name}</span>
                {r.source === "series" && <span className="pill">auto</span>}
              </div>
              <div className="muted small" style={{ marginTop: 3 }}>
                {describe(r)}
              </div>
            </div>
            <button
              className="btn-small btn-secondary"
              style={{ width: "auto" }}
              onClick={() => toggle.mutate(r)}
            >
              {r.enabled ? "Disable" : "Enable"}
            </button>
            <button
              className="btn-small btn-secondary"
              style={{ width: "auto" }}
              aria-label="Delete rule"
              onClick={() => remove.mutate(r.id)}
            >
              <CloseIcon size={13} />
            </button>
          </div>
        ))}
        {list.length === 0 && (
          <p className="muted">
            No rules yet. Rules are handy for vendors the AI keeps guessing wrong, or for
            splitting one vendor by amount.
          </p>
        )}
      </div>
    </>
  );
}
