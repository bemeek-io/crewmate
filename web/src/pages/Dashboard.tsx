import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { get, fmtCents } from "../api/client";
import type { MemberAccounts, Txn } from "../api/types";
import TxnRow from "../components/TxnRow";

function ago(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
}

export default function Dashboard() {
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => get<{ members: MemberAccounts[] }>("/api/accounts"),
    refetchInterval: 60_000,
  });
  const recent = useQuery({
    queryKey: ["transactions", "recent"],
    queryFn: () => get<{ transactions: Txn[] }>("/api/transactions?limit=5"),
    refetchInterval: 60_000,
  });
  const uncategorized = useQuery({
    queryKey: ["transactions", "uncat-count"],
    queryFn: () => get<{ transactions: Txn[] }>("/api/transactions?uncategorized=1&limit=20"),
    refetchInterval: 60_000,
  });

  const members = accounts.data?.members ?? [];
  const uncatCount = uncategorized.data?.transactions?.length ?? 0;

  return (
    <>
      <h1>Home</h1>

      {uncatCount > 0 && (
        <Link to="/transactions?uncategorized=1" style={{ textDecoration: "none" }}>
          <div className="banner">
            🏷️ {uncatCount >= 20 ? "20+" : uncatCount} transaction{uncatCount === 1 ? "" : "s"} in
            Misc — tap to categorize
          </div>
        </Link>
      )}

      {accounts.isLoading && (
        <div className="center" style={{ minHeight: "20dvh" }}>
          <div className="spinner" />
        </div>
      )}

      {!accounts.isLoading && members.length === 0 && (
        <div className="card">
          <p className="muted">
            No balances yet. They appear within a minute of connecting a Crew account.
          </p>
        </div>
      )}

      {members.map((m) => (
        <section key={m.user_id}>
          <h2>
            {m.first_name || "Member"} <span className="small muted">· updated {ago(m.fetched_at)}</span>
          </h2>
          {(m.accounts ?? []).map((a) => (
            <div className="card" key={a.id}>
              <div className="row spread">
                <div>
                  <div className="muted small">{a.name || a.type}</div>
                  <div className="balance-big">{fmtCents(a.overallBalance)}</div>
                </div>
              </div>
              {(a.subaccounts ?? []).length > 0 && (
                <div style={{ marginTop: 8 }}>
                  {(a.subaccounts ?? []).map((p) => {
                    const pct =
                      p.goal && p.goal > 0
                        ? Math.min(100, Math.round((p.overallBalance / p.goal) * 100))
                        : null;
                    return (
                      <div className="pocket" key={p.id}>
                        <div className="row spread">
                          <span>{p.name}</span>
                          <span className="txn-amount">{fmtCents(p.overallBalance)}</span>
                        </div>
                        {pct !== null && (
                          <>
                            <div className="goalbar">
                              <div style={{ width: `${pct}%` }} />
                            </div>
                            <div className="muted small">
                              {pct}% of {fmtCents(p.goal!)} goal
                            </div>
                          </>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          ))}
        </section>
      ))}

      <div className="row spread" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>Recent activity</h2>
        <Link to="/transactions" className="small">
          See all
        </Link>
      </div>
      <div className="card">
        {(recent.data?.transactions ?? []).map((t) => (
          <TxnRow txn={t} key={t.id} />
        ))}
        {recent.data && recent.data.transactions.length === 0 && (
          <p className="muted">No transactions yet.</p>
        )}
      </div>
    </>
  );
}
