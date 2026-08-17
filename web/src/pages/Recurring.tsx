import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, patch, fmtCents } from "../api/client";
import type { RecurringSeries } from "../api/types";

export default function Recurring() {
  const qc = useQueryClient();
  const series = useQuery({
    queryKey: ["recurring"],
    queryFn: () => get<{ series: RecurringSeries[] }>("/api/recurring"),
  });
  const dismiss = useMutation({
    mutationFn: ({ id, dismissed }: { id: string; dismissed: boolean }) =>
      patch(`/api/recurring/${id}`, { dismissed }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["recurring"] }),
  });

  const items = series.data?.series ?? [];
  const subs = items.filter((s) => s.is_subscription && !s.dismissed);
  const candidates = items.filter((s) => !s.is_subscription && !s.dismissed);
  const dismissed = items.filter((s) => s.dismissed);

  const renderItem = (s: RecurringSeries) => (
    <div className="row spread" style={{ padding: "10px 0" }} key={s.id}>
      <div className="grow">
        <div className="txn-title">{s.merchant_key}</div>
        <div className="muted small">
          {fmtCents(s.amount_cents)} · {s.cadence !== "unknown" ? s.cadence : "irregular"} ·{" "}
          {s.occurrence_count}×
        </div>
      </div>
      <button
        className="btn-small btn-secondary"
        style={{ width: "auto" }}
        onClick={() => dismiss.mutate({ id: s.id, dismissed: !s.dismissed })}
      >
        {s.dismissed ? "Restore" : "Dismiss"}
      </button>
    </div>
  );

  return (
    <>
      <h1>Recurring</h1>
      <h2>Subscriptions</h2>
      <div className="card">
        {subs.map(renderItem)}
        {subs.length === 0 && (
          <p className="muted">
            No subscriptions detected yet. A merchant becomes a subscription after 3 charges on a
            steady cadence.
          </p>
        )}
      </div>

      {candidates.length > 0 && (
        <>
          <h2>Possibly recurring</h2>
          <div className="card">{candidates.map(renderItem)}</div>
        </>
      )}

      {dismissed.length > 0 && (
        <>
          <h2>Dismissed</h2>
          <div className="card">{dismissed.map(renderItem)}</div>
        </>
      )}
    </>
  );
}
