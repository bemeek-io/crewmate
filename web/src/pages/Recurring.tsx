import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, patch, fmtCents } from "../api/client";
import type { RecurringSeries, Txn } from "../api/types";
import { ChevronRightIcon, ChevronDownIcon } from "../components/Icons";

interface SeriesResponse {
  series: RecurringSeries;
  transactions: Txn[];
}

function summarize(s: RecurringSeries): string {
  const cadence = s.cadence !== "unknown" ? s.cadence : "irregular";
  const amount =
    s.min_amount_cents === s.max_amount_cents
      ? fmtCents(s.typical_amount_cents)
      : `${fmtCents(s.max_amount_cents)}–${fmtCents(s.min_amount_cents)}`;
  return `${amount} · ${cadence} · ${s.occurrence_count}×`;
}

/** The charges behind a match, plus why it was classified the way it was. */
function SeriesDetail({ id }: { id: string }) {
  const detail = useQuery({
    queryKey: ["recurring", id, "transactions"],
    queryFn: () => get<SeriesResponse>(`/api/recurring/${id}/transactions`),
  });

  if (detail.isLoading) {
    return (
      <div className="center" style={{ minHeight: "12dvh" }}>
        <div className="spinner" />
      </div>
    );
  }
  const s = detail.data?.series;
  const txns = detail.data?.transactions ?? [];

  return (
    <div style={{ padding: "2px 0 12px" }}>
      {s && (
        <p className="muted small" style={{ marginBottom: 10 }}>
          {s.kind === "subscription"
            ? "Same amount on a steady schedule"
            : "Repeats at this merchant, but the amount varies"}
          {" — "}
          amount varies {s.amount_spread_pct}%, timing varies {s.interval_spread_pct}%
          {(s.cadence === "monthly" || s.cadence === "quarterly" || s.cadence === "yearly") &&
            `, billed within ${s.day_spread_days} day${s.day_spread_days === 1 ? "" : "s"} of the same date`}
          .
        </p>
      )}
      {txns.map((t) => (
        <Link to={`/transactions/${t.id}`} key={t.id} className="txn-row" style={{ paddingLeft: 2 }}>
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
  const subs = items.filter((s) => s.kind === "subscription" && !s.dismissed);
  const recurring = items.filter((s) => s.kind === "recurring" && !s.dismissed);
  const dismissed = items.filter((s) => s.dismissed);

  const renderItem = (s: RecurringSeries) => {
    const expanded = open === s.id;
    return (
      <div key={s.id} className="series-row">
        <div className="row spread" style={{ padding: "10px 0" }}>
          <button
            className="row grow series-toggle"
            onClick={() => setOpen(expanded ? null : s.id)}
            aria-expanded={expanded}
          >
            <span className="icon-muted" style={{ lineHeight: 0 }}>
              {expanded ? <ChevronDownIcon size={16} /> : <ChevronRightIcon size={16} />}
            </span>
            <span className="grow">
              <span className="txn-title">{s.merchant_key}</span>
              <span className="muted small" style={{ display: "block" }}>
                {summarize(s)}
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

      <h2>Subscriptions</h2>
      <p className="muted small" style={{ marginTop: -8, marginBottom: 12 }}>
        The same amount, charged on a steady schedule.
      </p>
      <div className="card">
        {subs.map(renderItem)}
        {subs.length === 0 && (
          <p className="muted">
            No subscriptions detected yet. It takes three charges of the same amount on a
            consistent schedule.
          </p>
        )}
      </div>

      {recurring.length > 0 && (
        <>
          <h2>Recurring spending</h2>
          <p className="muted small" style={{ marginTop: -8, marginBottom: 12 }}>
            Regular trips to the same place, but the amount changes each time.
          </p>
          <div className="card">{recurring.map(renderItem)}</div>
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
