import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { get, fmtCents } from "../api/client";
import type { CashFlow as CashFlowData, CashFlowEntry, VendorSpend } from "../api/types";
import TxnList from "../components/TxnList";
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

/**
 * A category's vendors, and beneath each, its transactions.
 *
 * Two levels rather than one: the total answers "how much", but the next
 * question is always "to whom", and a flat list of every transaction buries
 * that under repetition — twelve Netflix charges say less than one Netflix
 * line carrying a total.
 */
function EntryVendors({
  entry,
  direction,
  range,
  since,
  until,
}: {
  entry: CashFlowEntry;
  direction: "income" | "expense";
  range: string;
  since: string;
  until: string;
}) {
  const [open, setOpen] = useState<string | null>(null);

  const query = new URLSearchParams({ range, direction });
  // A null category is genuinely uncategorized, which the API models as
  // `uncategorized`. A category the family happens to name "Misc" is a real
  // category and is not this.
  if (entry.category_id) query.set("category", entry.category_id);
  else query.set("uncategorized", "1");

  const q = useQuery({
    queryKey: ["cashflow", "vendors", query.toString()],
    queryFn: () => get<{ vendors: VendorSpend[] }>(`/api/cashflow/vendors?${query}`),
  });

  if (q.isLoading) {
    return (
      <div className="center" style={{ minHeight: "8dvh" }}>
        <div className="spinner" />
      </div>
    );
  }
  const vendors = q.data?.vendors ?? [];
  return (
    <div style={{ padding: "2px 0 10px 26px" }}>
      {vendors.map((v) => {
        const expanded = open === v.merchant_key;
        // The category has to travel down with the merchant. A vendor can be
        // filed under several categories — Costco splits across Groceries,
        // Gas and Dining — so filtering by merchant alone lists charges that
        // aren't part of the total being expanded.
        const txnParams = new URLSearchParams({
          merchant: v.merchant_key,
          direction,
          since: new Date(since).toISOString(),
          until: new Date(until).toISOString(),
        });
        if (entry.category_id) txnParams.set("category", entry.category_id);
        else txnParams.set("uncategorized", "1");
        return (
          <div key={v.merchant_key} className="series-row">
            <button
              className="row series-toggle"
              style={{ width: "100%", padding: "10px 0" }}
              onClick={() => setOpen(expanded ? null : v.merchant_key)}
              aria-expanded={expanded}
            >
              <span className="icon-muted" style={{ lineHeight: 0 }}>
                {expanded ? <ChevronDownIcon size={15} /> : <ChevronRightIcon size={15} />}
              </span>
              <span className="grow" style={{ textAlign: "left", minWidth: 0 }}>
                <span className="txn-title">{v.payee}</span>
                <span className="muted small" style={{ display: "block" }}>
                  {v.count} transaction{v.count === 1 ? "" : "s"}
                </span>
              </span>
              <span className="txn-amount">{fmtCents(v.cents)}</span>
            </button>
            {expanded && <TxnList params={txnParams} indent={22} />}
          </div>
        );
      })}
      {vendors.length === 0 && <p className="muted small">No transactions.</p>}
    </div>
  );
}

function Section({
  title,
  entries,
  total,
  direction,
  range,
  since,
  until,
}: {
  title: string;
  entries: CashFlowEntry[];
  total: number;
  direction: "income" | "expense";
  range: string;
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
                    <span className="txn-title">{e.category_name || "Uncategorized"}</span>
                  </span>
                  <span className="muted small" style={{ display: "block" }}>
                    {e.count} transaction{e.count === 1 ? "" : "s"} · {pct}%
                  </span>
                </span>
                <span className="txn-amount">{fmtCents(e.cents)}</span>
              </button>
              {expanded && (
                <EntryVendors
                  entry={e}
                  direction={direction}
                  range={range}
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
            range={range}
            since={d.start}
            until={d.end}
          />
          <Section
            title="Income"
            entries={d.income}
            total={d.income_cents}
            direction="income"
            range={range}
            since={d.start}
            until={d.end}
          />
        </>
      )}
    </>
  );
}
