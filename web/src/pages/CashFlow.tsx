import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { get, fmtCents } from "../api/client";
import type { CashFlow as CashFlowData, CashFlowEntry, Txn } from "../api/types";
import { ChevronRightIcon, ChevronDownIcon } from "../components/Icons";

const RANGES = [
  { key: "1w", label: "1W" },
  { key: "1m", label: "1M" },
  { key: "3m", label: "3M" },
  { key: "6m", label: "6M" },
  { key: "1y", label: "1Y" },
];

const fmtDate = (iso: string) =>
  new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });

/** A row's transactions, fetched only once the row is expanded. */
function EntryTransactions({
  entry,
  direction,
  since,
  until,
}: {
  entry: CashFlowEntry;
  direction: "income" | "expense";
  since: string;
  until: string;
}) {
  const params = new URLSearchParams({
    direction,
    since: new Date(since).toISOString(),
    until: new Date(until).toISOString(),
    limit: "100",
  });
  // A null category means Misc, which the API models as `uncategorized`.
  if (entry.category_id) params.set("category", entry.category_id);
  else params.set("uncategorized", "1");

  const q = useQuery({
    queryKey: ["cashflow-txns", params.toString()],
    queryFn: () => get<{ transactions: Txn[] }>(`/api/transactions?${params}`),
  });

  if (q.isLoading) {
    return (
      <div className="center" style={{ minHeight: "8dvh" }}>
        <div className="spinner" />
      </div>
    );
  }
  const txns = q.data?.transactions ?? [];
  return (
    <div style={{ padding: "2px 0 10px 26px" }}>
      {txns.map((t) => (
        <Link to={`/transactions/${t.id}`} key={t.id} className="txn-row" style={{ paddingLeft: 2 }}>
          <div className="grow">
            <div className="txn-title">{t.payee || "Transaction"}</div>
            <div className="muted small">
              {new Date(t.occurred_at).toLocaleDateString(undefined, {
                month: "short",
                day: "numeric",
              })}
              {t.pending && " · pending"}
            </div>
          </div>
          <div className="txn-amount">{fmtCents(t.amount_cents, true)}</div>
        </Link>
      ))}
      {txns.length === 0 && <p className="muted small">No transactions.</p>}
      {txns.length === 100 && (
        <p className="muted small">Showing the first 100 — open Activity for the full list.</p>
      )}
    </div>
  );
}

function Section({
  title,
  entries,
  total,
  direction,
  since,
  until,
}: {
  title: string;
  entries: CashFlowEntry[];
  total: number;
  direction: "income" | "expense";
  since: string;
  until: string;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const key = (e: CashFlowEntry) => e.category_id ?? "misc";

  return (
    <>
      <div className="section-header">
        <h2>{title}</h2>
        <span className="txn-amount">{fmtCents(total)}</span>
      </div>
      <div className="card">
        {entries.map((e) => {
          const id = key(e);
          const expanded = open === id;
          // Share of the section, so the biggest lines read at a glance.
          const pct = total > 0 ? Math.round((e.cents / total) * 100) : 0;
          return (
            <div key={id} className="series-row">
              <button
                className="row series-toggle"
                style={{ width: "100%", padding: "12px 0 10px" }}
                onClick={() => setOpen(expanded ? null : id)}
                aria-expanded={expanded}
              >
                <span className="icon-muted" style={{ lineHeight: 0 }}>
                  {expanded ? <ChevronDownIcon size={16} /> : <ChevronRightIcon size={16} />}
                </span>
                <span className="grow" style={{ textAlign: "left", minWidth: 0 }}>
                  <span className="row" style={{ gap: 7 }}>
                    <span
                      className="cat-dot"
                      style={{ background: e.color || "var(--border)" }}
                      aria-hidden="true"
                    />
                    <span className="txn-title">{e.category_name || "Misc"}</span>
                  </span>
                  <span className="muted small" style={{ display: "block" }}>
                    {e.count} transaction{e.count === 1 ? "" : "s"} · {pct}%
                  </span>
                </span>
                <span className="txn-amount">{fmtCents(e.cents)}</span>
              </button>
              {expanded && (
                <EntryTransactions
                  entry={e}
                  direction={direction}
                  since={since}
                  until={until}
                />
              )}
            </div>
          );
        })}
        {entries.length === 0 && <p className="muted">Nothing in this window.</p>}
      </div>
    </>
  );
}

export default function CashFlow() {
  const [range, setRange] = useState("1m");
  const report = useQuery({
    queryKey: ["cashflow", range],
    queryFn: () => get<CashFlowData>(`/api/cashflow?range=${range}`),
  });

  const d = report.data;
  const surplus = (d?.net_cents ?? 0) >= 0;

  return (
    <>
      <h1>Cash flow</h1>

      <div className="chips">
        {RANGES.map((r) => (
          <button
            key={r.key}
            className={`chip ${range === r.key ? "on" : ""}`}
            onClick={() => setRange(r.key)}
          >
            {r.label}
          </button>
        ))}
      </div>

      {report.isLoading && (
        <div className="center" style={{ minHeight: "30dvh" }}>
          <div className="spinner" />
        </div>
      )}

      {d && (
        <>
          <div className="card">
            <div className="muted small">
              {fmtDate(d.start)} – {fmtDate(d.end)}
            </div>
            <div className={`balance-big ${surplus ? "surplus" : "deficit"}`}>
              {surplus ? "+" : "−"}
              {fmtCents(Math.abs(d.net_cents))}
            </div>
            <div className="muted small">{surplus ? "Surplus" : "Deficit"}</div>
            <div className="row spread" style={{ marginTop: 12 }}>
              <span className="muted small">In {fmtCents(d.income_cents)}</span>
              <span className="muted small">Out {fmtCents(d.expense_cents)}</span>
            </div>
            {/* Proportion of income consumed by spending. */}
            <div className="goalbar" style={{ marginTop: 6 }}>
              <div
                className={surplus ? "" : "over"}
                style={{
                  width: `${
                    d.income_cents > 0
                      ? Math.min(100, Math.round((d.expense_cents / d.income_cents) * 100))
                      : d.expense_cents > 0
                        ? 100
                        : 0
                  }%`,
                }}
              />
            </div>
          </div>

          <Section
            title="Expenses"
            entries={d.expenses}
            total={d.expense_cents}
            direction="expense"
            since={d.start}
            until={d.end}
          />
          <Section
            title="Income"
            entries={d.income}
            total={d.income_cents}
            direction="income"
            since={d.start}
            until={d.end}
          />
        </>
      )}
    </>
  );
}
