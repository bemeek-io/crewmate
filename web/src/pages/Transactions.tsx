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

export default function Transactions() {
  const [params, setParams] = useSearchParams();
  const uncategorized = params.get("uncategorized") === "1";
  const selected = (params.get("category") ?? "").split(",").filter(Boolean);
  const urlQuery = params.get("q") ?? "";

  // Local input state so typing stays responsive; the URL (and the request)
  // follows a short debounce behind it.
  const [term, setTerm] = useState(urlQuery);
  useEffect(() => setTerm(urlQuery), [urlQuery]);
  useEffect(() => {
    if (term === urlQuery) return;
    const t = setTimeout(() => {
      const next = new URLSearchParams(params);
      if (term.trim()) next.set("q", term.trim());
      else next.delete("q");
      setParams(next, { replace: true });
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term]);

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });

  const query = useInfiniteQuery({
    queryKey: ["transactions", { uncategorized, selected: selected.join(","), q: urlQuery }],
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: String(PAGE) });
      if (pageParam) qs.set("before", pageParam);
      if (uncategorized) qs.set("uncategorized", "1");
      if (selected.length) qs.set("category", selected.join(","));
      if (urlQuery) qs.set("q", urlQuery);
      return get<Page>(`/api/transactions?${qs}`);
    },
    initialPageParam: "",
    getNextPageParam: (last) => (last.transactions.length === PAGE ? last.next_cursor : undefined),
    refetchInterval: 60_000,
  });

  const txns = query.data?.pages.flatMap((p) => p.transactions) ?? [];
  const filtersActive = uncategorized || selected.length > 0 || !!urlQuery;

  function update(fn: (p: URLSearchParams) => void) {
    const next = new URLSearchParams(params);
    fn(next);
    setParams(next, { replace: true });
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

      <div className="chips">
        <button
          className={`chip ${uncategorized ? "on" : ""}`}
          onClick={() =>
            update((p) => (uncategorized ? p.delete("uncategorized") : p.set("uncategorized", "1")))
          }
        >
          Misc
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
