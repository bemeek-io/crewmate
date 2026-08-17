import { useSearchParams } from "react-router-dom";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { Category, Txn } from "../api/types";
import TxnRow from "../components/TxnRow";

interface Page {
  transactions: Txn[];
  next_cursor: string;
}

export default function Transactions() {
  const [params, setParams] = useSearchParams();
  const uncategorized = params.get("uncategorized") === "1";
  const category = params.get("category") ?? "";

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });

  const query = useInfiniteQuery({
    queryKey: ["transactions", { uncategorized, category }],
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: "50" });
      if (pageParam) qs.set("before", pageParam);
      if (uncategorized) qs.set("uncategorized", "1");
      if (category) qs.set("category", category);
      return get<Page>(`/api/transactions?${qs}`);
    },
    initialPageParam: "",
    getNextPageParam: (last) =>
      last.transactions.length === 50 ? last.next_cursor : undefined,
    refetchInterval: 60_000,
  });

  const txns = query.data?.pages.flatMap((p) => p.transactions) ?? [];

  return (
    <>
      <h1>Activity</h1>
      <div className="row" style={{ marginBottom: 12, flexWrap: "wrap", gap: 8 }}>
        <button
          className={`btn-small ${uncategorized ? "" : "btn-secondary"}`}
          style={{ width: "auto" }}
          onClick={() => {
            const next = new URLSearchParams(params);
            if (uncategorized) next.delete("uncategorized");
            else next.set("uncategorized", "1");
            setParams(next, { replace: true });
          }}
        >
          Misc only
        </button>
        <select
          style={{ width: "auto", flex: 1, marginBottom: 0 }}
          value={category}
          onChange={(e) => {
            const next = new URLSearchParams(params);
            if (e.target.value) next.set("category", e.target.value);
            else next.delete("category");
            setParams(next, { replace: true });
          }}
        >
          <option value="">All categories</option>
          {(categories.data?.categories ?? []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.emoji ? `${c.emoji} ` : ""}
              {c.name}
            </option>
          ))}
        </select>
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
        {!query.isLoading && txns.length === 0 && <p className="muted">Nothing here yet.</p>}
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
