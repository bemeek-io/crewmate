import { Link } from "react-router-dom";
import { fmtCents } from "../api/client";
import type { Txn } from "../api/types";

/**
 * A transaction's category is its Crew note matched against the family's
 * category list. Anything that doesn't resolve reads as "Misc" — with the raw
 * note shown when there is one, so a personal annotation stays visible.
 */
export function categoryLabel(txn: Txn): string {
  if (txn.category_name) return txn.category_name;
  return txn.note ? `Misc · ${txn.note}` : "Misc";
}

export default function TxnRow({ txn }: { txn: Txn }) {
  const initial = (txn.payee || "?").charAt(0).toUpperCase();
  return (
    <Link to={`/transactions/${txn.id}`} className="txn-row">
      <div className="txn-logo">
        {txn.image_url ? <img src={txn.image_url} alt="" loading="lazy" /> : initial}
      </div>
      <div className="grow">
        <div className="txn-title">{txn.payee || "Transaction"}</div>
        <div className="muted small">
          {new Date(txn.occurred_at).toLocaleDateString(undefined, {
            month: "short",
            day: "numeric",
          })}
          {txn.pending && " · pending"} · {categoryLabel(txn)}
          {txn.can_add_category && " · new note"}
        </div>
      </div>
      <div className={`txn-amount ${txn.amount_cents > 0 ? "pos" : "neg"}`}>
        {fmtCents(txn.amount_cents, true)}
      </div>
    </Link>
  );
}
