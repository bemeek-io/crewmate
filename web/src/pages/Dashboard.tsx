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

/**
 * Each member has exactly one real debit card. Crew also issues a virtual card
 * per merchant — dozens of them — but those are a Crew-side concern, not
 * something to manage from the home screen, so only the physical card is shown.
 */
function debitCard(cards: Card[] | null): Card | null {
  return (cards ?? []).find((c) => c.status === "ACTIVATED" && c.form_factor === "PHYSICAL") ?? null;
}

export default function Dashboard() {
  const qc = useQueryClient();
  const [error, setError] = useState("");

  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => get<{ members: MemberAccounts[] }>("/api/accounts"),
    refetchInterval: 20_000,
  });
  const recent = useQuery({
    queryKey: ["transactions", "recent"],
    queryFn: () => get<{ transactions: Txn[] }>("/api/transactions?limit=5"),
    refetchInterval: 20_000,
  });
  const uncategorized = useQuery({
    queryKey: ["transactions", "uncat-count"],
    queryFn: () => get<{ transactions: Txn[] }>("/api/transactions?uncategorized=1&limit=20"),
    refetchInterval: 20_000,
  });

  const movePocket = useMutation({
    mutationFn: (v: { cardID: string; subaccountID: string }) =>
      put(`/api/cards/${v.cardID}/pocket`, { subaccount_id: v.subaccountID }),
    onSuccess: () => {
      setError("");
      // The move happens in Crew; the snapshot catches up within a poll.
      setTimeout(() => qc.invalidateQueries({ queryKey: ["accounts"] }), 1500);
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not move the card"),
  });

  // The pocket a move is in flight to, so it can show a spinner-ish badge
  // until the next snapshot lands.
  const movingTo = movePocket.isPending ? movePocket.variables?.subaccountID : null;

  const members = accounts.data?.members ?? [];
  const uncatCount = uncategorized.data?.transactions?.length ?? 0;
  // Every member of a Crew household sees the same accounts, so showing each
  // member's snapshot listed the same balances once per person. Show only your
  // own — the balances are shared, and the card is yours.
  const me_id = me.data?.user.id;
  const visible = members
    .filter((m) => !me_id || m.user_id === me_id)
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
              {uncatCount >= 20 ? "20+" : uncatCount} transaction{uncatCount === 1 ? "" : "s"}{" "}
              uncategorized — tap to file
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
        // Everyone's card badge is visible, but only your own moves — the
        // write goes through your Crew connection.
        const isSelf = m.user_id === me.data?.user.id;
        const card = debitCard(m.cards);
        const canMove = isSelf && card !== null;
        return (
          <section key={m.user_id}>
            {/* No name: these are the household's shared accounts, not this
                member's, and only your own snapshot is rendered. */}
            <h2>
              Balances <span className="small muted">· updated {ago(m.fetched_at)}</span>
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
                      const hasCard = card !== null && card.subaccount_id === p.id;
                      // Tapping any other pocket moves the card straight there
                      // — no card picker, the badge just relocates.
                      const isTarget = canMove && !hasCard;
                      const landing = movingTo === p.id;
                      return (
                        <div
                          className={`pocket ${isTarget ? "pocket-target" : ""}`}
                          key={p.id}
                          onClick={() => {
                            if (isTarget && card) {
                              movePocket.mutate({ cardID: card.id, subaccountID: p.id });
                            }
                          }}
                          role={isTarget ? "button" : undefined}
                        >
                          <div className="row spread">
                            <span className="row grow" style={{ gap: 8 }}>
                              {p.name}
                              {hasCard && (
                                <span className="card-badge" title={`Card •••• ${card!.last_four}`}>
                                  <CardIcon size={14} />
                                  {card!.last_four}
                                </span>
                              )}
                              {landing && (
                                <span className="card-badge">
                                  <CardIcon size={14} />
                                  moving…
                                </span>
                              )}
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
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            ))}

            {canMove && (
              <p className="muted small" style={{ marginTop: -4 }}>
                <CardIcon size={13} /> Tap a pocket to spend your card from it.
              </p>
            )}
            {isSelf && error && <div className="error">{error}</div>}
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
