import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { get, fmtCents } from "../api/client";
import type { Txn } from "../api/types";

const PAGE = 100;

/**
 * The transactions behind a drill-down, fetched only once it's opened.
 *
 * Deliberately not the full Activity row: at this depth the merchant and
 * category are already established by the line above, so the date and amount
 * are what's left to say.
 */
export default function TxnList({ params, indent = 26 }: { params: URLSearchParams; indent?: number }) {
  const qs = new URLSearchParams(params);
  qs.set("limit", String(PAGE));

  const q = useQuery({
    queryKey: ["transactions", "drilldown", qs.toString()],
    queryFn: () => get<{ transactions: Txn[] }>(`/api/transactions?${qs}`),
  });

  if (q.isLoading) {
    return (
      <div className="center" style={{ minHeight: "6dvh" }}>
        <div className="spinner" />
      </div>
    );
  }
  const txns = q.data?.transactions ?? [];
  return (
    <div style={{ padding: `2px 0 10px ${indent}px` }}>
      {txns.map((t) => (
        <Link to={`/transactions/${t.id}`} key={t.id} className="txn-row" style={{ paddingLeft: 2 }}>
          <div className="grow">
            <div className="txn-title">{t.payee || "Transaction"}</div>
            <div className="muted small">
              {new Date(t.occurred_at).toLocaleDateString(undefined, {
                month: "short",
                day: "numeric",
                year: "numeric",
              })}
              {t.pending && " · pending"}
              {t.category_name ? ` · ${t.category_name}` : ""}
            </div>
          </div>
          <div className="txn-amount">{fmtCents(t.amount_cents, true)}</div>
        </Link>
      ))}
      {txns.length === 0 && <p className="muted small">No transactions.</p>}
      {txns.length === PAGE && (
        <p className="muted small">Showing the first {PAGE} — open Activity for the full list.</p>
      )}
    </div>
  );
}
