import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, fmtCents } from "../api/client";
import type { SubscriptionSpend as Data } from "../api/types";
import TxnList from "./TxnList";
import { ChevronRightIcon, ChevronDownIcon, RefreshIcon } from "./Icons";

const RANGES = [
  { key: "1m", label: "1M" },
  { key: "3m", label: "3M" },
  { key: "6m", label: "6M" },
  { key: "1y", label: "1Y" },
];

/**
 * What the subscriptions actually cost, by vendor.
 *
 * Counted from the series classification rather than the Subscription
 * category, because those diverge as soon as anyone files a subscription
 * somewhere more useful — Tech, say. A category total can't answer "what am I
 * paying to subscribe to things"; this can.
 */
export default function SubscriptionSpend() {
  const [range, setRange] = useState("1m");
  const [open, setOpen] = useState<string | null>(null);
  const qc = useQueryClient();

  const spend = useQuery({
    queryKey: ["recurring", "spend", range],
    queryFn: () => get<Data>(`/api/recurring/spend?range=${range}`),
  });

  const rediscover = useMutation({
    mutationFn: () => post("/api/recurring/reclassify", {}),
    // The totals themselves are the feedback: refetching re-renders them.
    onSuccess: () => qc.invalidateQueries({ queryKey: ["recurring"] }),
  });

  const d = spend.data;
  const vendors = d?.vendors ?? [];

  return (
    <>
      <div className="section-header">
        <h2>Subscription spend</h2>
        {/* Detection otherwise only re-runs when a replica picks up a Crew
            connection, so classifications go stale after a change to how
            things are detected, with no way to refresh them. */}
        <button
          className="icon-btn"
          aria-label="Re-detect subscriptions"
          title="Re-detect subscriptions"
          disabled={rediscover.isPending}
          onClick={() => rediscover.mutate()}
        >
          <RefreshIcon size={17} />
        </button>
      </div>
      <p className="muted small" style={{ marginTop: -8, marginBottom: 12 }}>
        Same amount, same point in the cycle — whatever category they're filed under.
      </p>

      <div className="card">
        <div className="chips" style={{ marginTop: 0 }}>
          {RANGES.map((r) => (
            <button
              key={r.key}
              className={`chip sm ${range === r.key ? "on" : ""}`}
              onClick={() => setRange(r.key)}
            >
              {r.label}
            </button>
          ))}
        </div>

        {spend.isLoading && (
          <div className="center" style={{ minHeight: "10dvh" }}>
            <div className="spinner" />
          </div>
        )}

        {d && (
          <>
            <div className="balance-big">{fmtCents(d.total_cents)}</div>
            <div className="muted small" style={{ marginBottom: 6 }}>
              across {vendors.length} subscription{vendors.length === 1 ? "" : "s"} ·{" "}
              {d.range_label.toLowerCase()}
            </div>

            {vendors.map((v) => {
              const expanded = open === v.merchant_key;
              const params = new URLSearchParams({
                merchant: v.merchant_key,
                direction: "expense",
                since: new Date(d.start).toISOString(),
                until: new Date(d.end).toISOString(),
              });
              return (
                <div className="series-row" key={v.merchant_key}>
                  <button
                    className="row series-toggle"
                    style={{ width: "100%", padding: "11px 0" }}
                    onClick={() => setOpen(expanded ? null : v.merchant_key)}
                    aria-expanded={expanded}
                  >
                    <span className="icon-muted" style={{ lineHeight: 0 }}>
                      {expanded ? <ChevronDownIcon size={16} /> : <ChevronRightIcon size={16} />}
                    </span>
                    <span className="grow" style={{ textAlign: "left", minWidth: 0 }}>
                      <span className="txn-title">{v.payee}</span>
                      <span className="muted small" style={{ display: "block" }}>
                        {v.count} charge{v.count === 1 ? "" : "s"}
                      </span>
                    </span>
                    <span className="txn-amount">{fmtCents(v.cents)}</span>
                  </button>
                  {expanded && <TxnList params={params} />}
                </div>
              );
            })}

            {vendors.length === 0 && (
              <p className="muted">No subscription charges in this window.</p>
            )}

          </>
        )}
      </div>
    </>
  );
}
