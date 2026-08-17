import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, patch, fmtCents } from "../api/client";
import type { RecurringSeries, Txn } from "../api/types";
import { ChevronRightIcon, ChevronDownIcon } from "../components/Icons";

interface SeriesDetail {
  series: RecurringSeries & { amount_tolerance: number };
  transactions: Txn[];
}

function describe(s: RecurringSeries): string {
  const cadence = s.cadence !== "unknown" ? s.cadence : "irregular";
  return `${fmtCents(s.amount_cents)} · ${cadence} · seen ${s.occurrence_count}×`;
}

/** The occurrences behind one detected series — the evidence for the match. */
function SeriesDetail({ id }: { id: string }) {
  const detail = useQuery({
    queryKey: ["recurring", id, "transactions"],
    queryFn: () => get<SeriesDetail>(`/api/recurring/${id}/transactions`),
  });

  if (detail.isLoading) {
    return (
      <div className="center" style={{ minHeight: "12dvh" }}>
        <div className="spinner" />
      </div>
    );
  }
  const txns = detail.data?.transactions ?? [];
  const tol = detail.data?.series.amount_tolerance ?? 0;

  return (
    <div style={{ padding: "4px 0 10px" }}>
      <p className="muted small" style={{ marginBottom: 10 }}>
        Matched on merchant and amount within {fmtCents(tol)}
        {detail.data?.series.period_days
          ? `, about every ${detail.data.series.period_days} days`
          : ""}
        .
      </p>
      {txns.map((t) => (
        <Link
          to={`/transactions/${t.id}`}
          key={t.id}
          className="txn-row"
          style={{ paddingLeft: 2 }}
        >
          <div className="grow">
            <div className="txn-title">{t.payee || "Transaction"}</div>
            <div className="muted small">
              {new Date(t.occurred_at).toLocaleDateString(undefined, {
                year: "numeric",
                month: "short",
                day: "numeric",
              })}
              {t.pending && " · pending"}
              {t.category_name ? ` · ${t.category_name}` : t.note ? ` · ${t.note}` : " · Misc"}
            </div>
          </div>
          <div className="txn-amount">{fmtCents(t.amount_cents, true)}</div>
        </Link>
      ))}
      {txns.length === 0 && <p className="muted small">No matching transactions.</p>}
    </div>
  );
}

export default function Recurring() {
  const qc = useQueryClient();
  const [open, setOpen] = useState<string | null>(null);

  const series = useQuery({
    queryKey: ["recurring"],
    queryFn: () => get<{ series: RecurringSeries[] }>("/api/recurring"),
  });
  const dismiss = useMutation({
    mutationFn: ({ id, dismissed }: { id: string; dismissed: boolean }) =>
      patch(`/api/recurring/${id}`, { dismissed }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["recurring"] }),
  });

  const items = series.data?.series ?? [];
  const subs = items.filter((s) => s.is_subscription && !s.dismissed);
  const candidates = items.filter((s) => !s.is_subscription && !s.dismissed);
  const dismissed = items.filter((s) => s.dismissed);

  const renderItem = (s: RecurringSeries) => {
    const expanded = open === s.id;
    return (
      <div key={s.id} style={{ borderBottom: "1px solid var(--border)" }}>
        <div className="row spread" style={{ padding: "10px 0" }}>
          <button
            className="row grow"
            style={{
              background: "none",
              border: 0,
              padding: 0,
              width: "auto",
              color: "inherit",
              textAlign: "left",
              gap: 10,
              cursor: "pointer",
            }}
            onClick={() => setOpen(expanded ? null : s.id)}
            aria-expanded={expanded}
          >
            <span className="icon-muted" style={{ lineHeight: 0 }}>
              {expanded ? <ChevronDownIcon size={16} /> : <ChevronRightIcon size={16} />}
            </span>
            <span className="grow">
              <span className="txn-title">{s.merchant_key}</span>
              <span className="muted small" style={{ display: "block" }}>
                {describe(s)}
              </span>
            </span>
          </button>
          <button
            className="btn-small btn-secondary"
            style={{ width: "auto" }}
            onClick={() => dismiss.mutate({ id: s.id, dismissed: !s.dismissed })}
          >
            {s.dismissed ? "Restore" : "Dismiss"}
          </button>
        </div>
        {expanded && <SeriesDetail id={s.id} />}
      </div>
    );
  };

  return (
    <>
      <h1>Recurring</h1>
      <p className="muted small" style={{ marginBottom: 12 }}>
        Detected by grouping charges from the same merchant with a similar amount and a steady
        gap between them. Tap one to see the transactions behind it.
      </p>

      <h2>Subscriptions</h2>
      <div className="card">
        {subs.map(renderItem)}
        {subs.length === 0 && (
          <p className="muted">
            No subscriptions detected yet. A merchant becomes a subscription after 3 charges on a
            steady cadence.
          </p>
        )}
      </div>

      {candidates.length > 0 && (
        <>
          <h2>Possibly recurring</h2>
          <div className="card">{candidates.map(renderItem)}</div>
        </>
      )}

      {dismissed.length > 0 && (
        <>
          <h2>Dismissed</h2>
          <div className="card">{dismissed.map(renderItem)}</div>
        </>
      )}
    </>
  );
}
