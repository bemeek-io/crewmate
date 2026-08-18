import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Category, Txn } from "../api/types";
import TxnRow from "../components/TxnRow";
import { CloseIcon } from "../components/Icons";

interface Page {
  transactions: Txn[];
  next_cursor: string;
}

const PAGE = 50;

// Timeframe filter. The empty key is All time and is the default, so a bare
// /transactions URL is unfiltered.
const RANGES = [
  { key: "", label: "All time" },
  { key: "1w", label: "1W" },
  { key: "1m", label: "1M" },
  { key: "3m", label: "3M" },
  { key: "6m", label: "6M" },
  { key: "1y", label: "1Y" },
];

/**
 * Start of the window as an ISO timestamp, or null for All time. Computed at
 * request time rather than stored, so a long-lived tab doesn't keep asking
 * about a window that has drifted into the past.
 */
function sinceFor(range: string): string | null {
  const d = new Date();
  switch (range) {
    case "1w":
      d.setDate(d.getDate() - 7);
      break;
    case "1m":
      d.setMonth(d.getMonth() - 1);
      break;
    case "3m":
      d.setMonth(d.getMonth() - 3);
      break;
    case "6m":
      d.setMonth(d.getMonth() - 6);
      break;
    case "1y":
      d.setFullYear(d.getFullYear() - 1);
      break;
    default:
      return null;
  }
  return d.toISOString();
}

export default function Transactions() {
  const [params, setParams] = useSearchParams();
  const uncategorized = params.get("uncategorized") === "1";
  const selected = (params.get("category") ?? "").split(",").filter(Boolean);
  const urlQuery = params.get("q") ?? "";
  const range = params.get("range") ?? "";

  // Local input state so typing stays responsive; the URL (and the request)
  // follows a short debounce behind it.
  //
  // The debounced write MUST use the functional form of setParams. Building it
  // from a captured `params` reads a snapshot from whenever `term` last
  // changed, so tapping a filter chip mid-debounce would be silently undone.
  const [term, setTerm] = useState(urlQuery);
  useEffect(() => setTerm(urlQuery), [urlQuery]);
  useEffect(() => {
    if (term === urlQuery) return;
    const t = setTimeout(() => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (term.trim()) next.set("q", term.trim());
          else next.delete("q");
          return next;
        },
        { replace: true }
      );
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term, urlQuery]);

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });

  const query = useInfiniteQuery({
    queryKey: [
      "transactions",
      { uncategorized, selected: selected.join(","), q: urlQuery, range },
    ],
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: String(PAGE) });
      if (pageParam) qs.set("before", pageParam);
      if (uncategorized) qs.set("uncategorized", "1");
      if (selected.length) qs.set("category", selected.join(","));
      if (urlQuery) qs.set("q", urlQuery);
      const since = sinceFor(range);
      if (since) qs.set("since", since);
      return get<Page>(`/api/transactions?${qs}`);
    },
    initialPageParam: "",
    getNextPageParam: (last) => (last.transactions.length === PAGE ? last.next_cursor : undefined),
    refetchInterval: 20_000,
  });

  const txns = query.data?.pages.flatMap((p) => p.transactions) ?? [];
  const filtersActive = uncategorized || selected.length > 0 || !!urlQuery || !!range;

  // Same reasoning as the debounce above: always derive from the latest params.
  function update(fn: (p: URLSearchParams) => void) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        fn(next);
        return next;
      },
      { replace: true }
    );
  }

  function toggleCategory(id: string) {
    const set = new Set(selected);
    if (set.has(id)) set.delete(id);
    else set.add(id);
    update((p) => {
      if (set.size) p.set("category", [...set].join(","));
      else p.delete("category");
    });
  }

  return (
    <>
      <h1>Activity</h1>

      <input
        type="search"
        inputMode="search"
        placeholder="Search merchant or note"
        value={term}
        onChange={(e) => setTerm(e.target.value)}
      />

      {/* Timeframe on its own row — it stays findable however many category
          chips the family accumulates. */}
      <div className="chips" style={{ marginBottom: 0 }}>
        {RANGES.map((r) => (
          <button
            key={r.key || "all"}
            className={`chip sm ${range === r.key ? "on" : ""}`}
            onClick={() =>
              update((p) => (r.key ? p.set("range", r.key) : p.delete("range")))
            }
          >
            {r.label}
          </button>
        ))}
      </div>

      <div className="filter-divider" />

      <div className="chips">
        <button
          className={`chip ${uncategorized ? "on" : ""}`}
          onClick={() =>
            update((p) => (uncategorized ? p.delete("uncategorized") : p.set("uncategorized", "1")))
          }
        >
          Uncategorized
        </button>
        {(categories.data?.categories ?? []).map((c) => {
          const on = selected.includes(c.id);
          return (
            <button
              key={c.id}
              className={`chip ${on ? "on" : ""}`}
              onClick={() => toggleCategory(c.id)}
            >
              <span
                className="swatch"
                style={c.color ? { background: c.color } : undefined}
              />
              {c.name}
            </button>
          );
        })}
        {filtersActive && (
          <button
            className="chip clear"
            onClick={() =>
              update((p) => {
                p.delete("uncategorized");
                p.delete("category");
                p.delete("q");
                p.delete("range");
              })
            }
          >
            <CloseIcon size={13} /> Clear
          </button>
        )}
      </div>

      <div className="card">
        {query.isLoading && (
          <div className="center" style={{ minHeight: "20dvh" }}>
            <div className="spinner" />
          </div>
        )}
        {txns.map((t) => (
          <TxnRow txn={t} key={t.id} />
        ))}
        {!query.isLoading && txns.length === 0 && (
          <p className="muted">
            {filtersActive ? "Nothing matches those filters." : "Nothing here yet."}
          </p>
        )}
      </div>

      {query.hasNextPage && (
        <button
          className="btn-secondary"
          onClick={() => query.fetchNextPage()}
          disabled={query.isFetchingNextPage}
        >
          {query.isFetchingNextPage ? "Loading…" : "Load more"}
        </button>
      )}
    </>
  );
}
