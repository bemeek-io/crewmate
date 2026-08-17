import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, put, fmtCents, ApiError } from "../api/client";
import type { Account, Card, MemberAccounts, Me, Txn } from "../api/types";
import TxnRow from "../components/TxnRow";
import { TagIcon, ChevronRightIcon, CardIcon } from "../components/Icons";

function ago(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
}

/**
 * Crew reports linked outside accounts as EXTERNAL_SPEND / EXTERNAL_SAVE /
 * EXTERNAL_OTHER. Those aren't Crew balances, so they're left off the home
 * screen — their transactions still flow through Activity.
 */
const isExternal = (a: Account) => a.type?.toUpperCase().startsWith("EXTERNAL");

const isLive = (c: Card) => c.status !== "CANCELED" && c.status !== "EXPIRED";

function cardLabel(c: Card): string {
  return c.last_four ? `•••• ${c.last_four}` : c.name || "Card";
}

export default function Dashboard() {
  const qc = useQueryClient();
  // Which card the member is currently relocating (their own cards only).
  const [moving, setMoving] = useState<Card | null>(null);
  const [error, setError] = useState("");

  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
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

  const movePocket = useMutation({
    mutationFn: (v: { cardID: string; subaccountID: string }) =>
      put(`/api/cards/${v.cardID}/pocket`, { subaccount_id: v.subaccountID }),
    onSuccess: () => {
      setMoving(null);
      setError("");
      // The move happens in Crew; the snapshot catches up within a poll.
      setTimeout(() => qc.invalidateQueries({ queryKey: ["accounts"] }), 1500);
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not move the card"),
  });

  const members = accounts.data?.members ?? [];
  const uncatCount = uncategorized.data?.transactions?.length ?? 0;
  const visible = members
    .map((m) => ({ ...m, accounts: (m.accounts ?? []).filter((a) => !isExternal(a)) }))
    .filter((m) => m.accounts.length > 0);

  return (
    <>
      <h1>Home</h1>

      {uncatCount > 0 && (
        <Link to="/transactions?uncategorized=1" style={{ textDecoration: "none" }}>
          <div className="banner row" style={{ gap: 10 }}>
            <TagIcon size={18} />
            <span className="grow">
              {uncatCount >= 20 ? "20+" : uncatCount} transaction{uncatCount === 1 ? "" : "s"} in
              Misc — tap to categorize
            </span>
            <ChevronRightIcon size={16} />
          </div>
        </Link>
      )}

      {accounts.isLoading && (
        <div className="center" style={{ minHeight: "20dvh" }}>
          <div className="spinner" />
        </div>
      )}

      {!accounts.isLoading && visible.length === 0 && (
        <div className="card">
          <p className="muted">
            No balances yet. They appear within a minute of connecting a Crew account.
          </p>
        </div>
      )}

      {visible.map((m) => {
        // Cards can only be moved on your own account — the write goes through
        // your Crew connection.
        const isSelf = m.user_id === me.data?.user.id;
        const cards = (m.cards ?? []).filter(isLive);
        return (
          <section key={m.user_id}>
            <h2>
              {m.first_name || "Member"}{" "}
              <span className="small muted">· updated {ago(m.fetched_at)}</span>
            </h2>
            {m.accounts.map((a) => (
              <div className="card" key={a.id}>
                {a.name && <div className="muted small">{a.name}</div>}
                <div className="balance-big">{fmtCents(a.overallBalance)}</div>
                {(a.subaccounts ?? []).length > 0 && (
                  <div style={{ marginTop: 10 }}>
                    {(a.subaccounts ?? []).map((p) => {
                      const pct =
                        p.goal && p.goal > 0
                          ? Math.min(100, Math.round((p.overallBalance / p.goal) * 100))
                          : null;
                      const here = cards.filter((c) => c.subaccount_id === p.id);
                      const isTarget = moving && moving.subaccount_id !== p.id;
                      return (
                        <div
                          className={`pocket ${isTarget ? "pocket-target" : ""}`}
                          key={p.id}
                          onClick={() => {
                            if (isTarget && moving) {
                              movePocket.mutate({ cardID: moving.id, subaccountID: p.id });
                            }
                          }}
                          role={isTarget ? "button" : undefined}
                        >
                          <div className="row spread">
                            <span className="row grow" style={{ gap: 8 }}>
                              {p.name}
                              {here.map((c) => (
                                <span className="card-badge" key={c.id} title={cardLabel(c)}>
                                  <CardIcon size={14} />
                                  {c.last_four}
                                </span>
                              ))}
                            </span>
                            <span className="txn-amount">{fmtCents(p.overallBalance)}</span>
                          </div>
                          {pct !== null && (
                            <>
                              <div className="goalbar">
                                <div style={{ width: `${pct}%` }} />
                              </div>
                              <div className="muted small goal-caption">
                                {pct}% of {fmtCents(p.goal!)} goal
                              </div>
                            </>
                          )}
                          {isTarget && (
                            <div className="small" style={{ marginTop: 6, color: "var(--accent)" }}>
                              Move {cardLabel(moving!)} here
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            ))}

            {isSelf && cards.length > 0 && (
              <div className="card">
                <div className="muted small" style={{ marginBottom: 8 }}>
                  {moving
                    ? "Tap the pocket this card should spend from."
                    : "Tap a card to move which pocket it spends from."}
                </div>
                <div className="chips" style={{ margin: 0 }}>
                  {cards.map((c) => (
                    <button
                      key={c.id}
                      className={`chip ${moving?.id === c.id ? "on" : ""}`}
                      onClick={() => setMoving(moving?.id === c.id ? null : c)}
                    >
                      <CardIcon size={14} />
                      {cardLabel(c)}
                      {c.subaccount_name ? ` · ${c.subaccount_name}` : ""}
                    </button>
                  ))}
                  {moving && (
                    <button className="chip clear" onClick={() => setMoving(null)}>
                      Cancel
                    </button>
                  )}
                </div>
                {movePocket.isPending && (
                  <div className="muted small" style={{ marginTop: 8 }}>
                    Moving…
                  </div>
                )}
                {error && <div className="error">{error}</div>}
              </div>
            )}
          </section>
        );
      })}

      <div className="section-header">
        <h2>Recent activity</h2>
        <Link to="/transactions">See all</Link>
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
