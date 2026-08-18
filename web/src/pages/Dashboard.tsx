import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation } from "@tanstack/react-query";
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
  const [error, setError] = useState("");
  // Where the card has been asked to go, held until the snapshot agrees.
  const [pendingPocket, setPendingPocket] = useState<string | null>(null);

  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => get<{ members: MemberAccounts[] }>("/api/accounts"),
    // The Crew write is queued, so poll harder while waiting on it rather
    // than leaving the card looking stuck for most of a minute.
    refetchInterval: pendingPocket ? 2_500 : 20_000,
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
    // The badge moves on tap and stays put. Tying it to the request instead
    // showed "moving…" for the few hundred milliseconds the call took, then
    // dropped it — the card only actually hopped a poll later, when the
    // snapshot caught up, which read as a glitch followed by a jump.
    onMutate: (v) => {
      setError("");
      setPendingPocket(v.subaccountID);
    },
    onError: (e) => {
      setPendingPocket(null);
      setError(e instanceof ApiError ? e.message : "Could not move the card");
    },
  });

  const members = accounts.data?.members ?? [];
  const uncatCount = uncategorized.data?.transactions?.length ?? 0;
  const myCard = debitCard(members.find((m) => m.user_id === me.data?.user.id)?.cards ?? null);

  // Let go once the snapshot reports the card where it was sent.
  useEffect(() => {
    if (pendingPocket && myCard?.subaccount_id === pendingPocket) setPendingPocket(null);
  }, [pendingPocket, myCard?.subaccount_id]);

  // A queued Crew write can fail after the request succeeded, and holding the
  // badge somewhere it never went is worse than admitting we don't know: give
  // up after a minute and show whatever the snapshot says.
  useEffect(() => {
    if (!pendingPocket) return;
    const t = setTimeout(() => setPendingPocket(null), 60_000);
    return () => clearTimeout(t);
  }, [pendingPocket]);
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
                      // Where the card is shown: the requested pocket while a
                      // move settles, otherwise where the snapshot says it is.
                      const shownPocket = (isSelf && pendingPocket) || card?.subaccount_id;
                      const hasCard = card !== null && shownPocket === p.id;
                      // Tapping any other pocket moves the card straight there
                      // — no card picker, the badge just relocates.
                      const isTarget = canMove && !hasCard;
                      const settling = hasCard && isSelf && pendingPocket === p.id;
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
                                <span
                                  className={`card-badge ${settling ? "settling" : ""}`}
                                  title={`Card •••• ${card!.last_four}`}
                                >
                                  <CardIcon size={14} />
                                  {card!.last_four}
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
