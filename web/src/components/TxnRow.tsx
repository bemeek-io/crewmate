import { Link } from "react-router-dom";
import { fmtCents } from "../api/client";
import type { Txn } from "../api/types";

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
          {txn.pending && " · pending"}
          {txn.category_name ? (
            <> · {txn.category_name}</>
          ) : txn.has_user_note ? (
            <> · “{txn.note}”</>
          ) : (
            <>
              {" "}
              · <span className="pill warn">tap to categorize</span>
            </>
          )}
        </div>
      </div>
      <div className={`txn-amount ${txn.amount_cents > 0 ? "pos" : "neg"}`}>
        {fmtCents(txn.amount_cents, true)}
      </div>
    </Link>
  );
}
