import { useEffect, useId, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { del, get, post, fmtCents } from "../api/client";
import type { InsightDismissal, Insights, Suggestion } from "../api/types";
import TxnList from "./TxnList";
import { ChevronDownIcon, ChevronRightIcon, SparkleIcon, WalletIcon } from "./Icons";
import { barPath, compact, dollars, monthDate, niceTicks, useWidth } from "./chartKit";

/** Months of projection drawn after the ones already spent. */
const AHEAD = 12;

const KIND_LABEL: Record<Suggestion["kind"], string> = {
  subscription: "Subscription",
  habit: "Regular spend",
  rising: "Rising",
};

const CADENCE: Record<string, string> = {
  weekly: "weekly",
  biweekly: "every two weeks",
  monthly: "monthly",
  quarterly: "quarterly",
  yearly: "yearly",
};

const monthRange = (d: Insights) => {
  const full = d.months.slice(0, -1);
  const fmt = (k: string) => monthDate(k).toLocaleDateString(undefined, { month: "short", year: "numeric" });
  return `${fmt(full[0])} – ${fmt(full[full.length - 1])}`;
};

const suggestionKey = (s: { subject_type: string; subject_key: string }) =>
  `${s.subject_type}:${s.subject_key}`;

/** One line under the name: what the spend is, at a glance. */
function summary(s: Suggestion): string {
  switch (s.kind) {
    case "subscription":
      return `${fmtCents(s.per_charge_cents)} ${CADENCE[s.cadence] ?? "per charge"}`;
    case "habit":
      return `About ${dollars(s.pace_cents)}/mo lately`;
    case "rising": {
      const pct = Math.round(((s.pace_cents - s.baseline_cents) / s.baseline_cents) * 100);
      return `Up ${pct}% to ${dollars(s.pace_cents)}/mo`;
    }
  }
}

/**
 * The claim, in full. Every figure is computed by the server from the
 * transactions listed underneath; nothing here comes from the AI.
 */
function Claim({ s, d }: { s: Suggestion; d: Insights }) {
  const range = monthRange(d);
  const charges = `${s.charges} charge${s.charges === 1 ? "" : "s"}`;
  switch (s.kind) {
    case "subscription":
      return (
        <p className="sclaim">
          You've paid <b>{fmtCents(s.last12_cents)}</b> for this over the last 12 months ({charges}, {range}). It
          bills {fmtCents(s.per_charge_cents)} {CADENCE[s.cadence] ?? "per charge"}, so cancelling saves about{" "}
          <b className="pos">{dollars(s.savings_cents)}</b> over the next 12 months.
        </p>
      );
    case "habit":
      return (
        <p className="sclaim">
          You've spent <b>{fmtCents(s.last12_cents)}</b> here over the last 12 months ({charges}, {range}), about{" "}
          {dollars(s.pace_cents)} a month over the last three. At that pace it's{" "}
          {dollars(s.projected_cents)} over the next 12 months. Stopping would save about{" "}
          <b className="pos">{dollars(s.savings_cents)}</b>; cutting it in half, about{" "}
          {dollars(s.savings_cents / 2)}.
        </p>
      );
    case "rising":
      return (
        <p className="sclaim">
          {s.title} averaged <b>{dollars(s.pace_cents)}</b> a month over the last three months, up from{" "}
          {dollars(s.baseline_cents)} in the six before. Getting back to {dollars(s.baseline_cents)} a month saves
          about <b className="pos">{dollars(s.savings_cents)}</b> over the next 12 months.
          {s.drivers && s.drivers.length > 0 && (
            <>
              {" "}
              Most of the increase:{" "}
              {s.drivers.map((dr, i) => (
                <span key={dr.merchant_key}>
                  {i > 0 && ", "}
                  {dr.payee} (+{dollars(dr.delta_cents)}/mo)
                </span>
              ))}
              .
            </>
          )}
        </p>
      );
  }
}

/**
 * What was spent each month, then the next twelve at the current pace — the
 * projection a savings figure is taken from, drawn so it can be checked.
 */
function EvidenceChart({ s, d }: { s: Suggestion; d: Insights }) {
  const [wrapRef, width] = useWidth<HTMLDivElement>();
  const hatch = `shatch-${useId().replace(/:/g, "")}`;
  const color = s.color || "var(--accent)";
  const n = s.cents.length;
  const total = n + AHEAD;
  const W = width || 320;
  const H = 150;
  const pad = { l: 36, r: 4, t: 8, b: 22 };
  const plotW = W - pad.l - pad.r;
  const plotH = H - pad.t - pad.b;
  const ticks = niceTicks(Math.max(...s.cents, s.pace_cents, s.baseline_cents));
  const top = ticks[ticks.length - 1] || 1;
  const y = (v: number) => pad.t + plotH - (v / top) * plotH;
  const slot = plotW / total;
  const bw = Math.min(14, slot * 0.62);
  const cx = (i: number) => pad.l + slot * (i + 0.5);
  const divider = pad.l + slot * n;
  const last = n - 1;

  // Month labels: the first spent month, the current one, and a year out.
  const withYear = (m: Date) =>
    `${m.toLocaleDateString(undefined, { month: "short" })} ’${String(m.getFullYear()).slice(2)}`;
  const now = monthDate(d.months[last]);
  const labels: [number, string][] = [
    [0, withYear(monthDate(d.months[0]))],
    [last, "Now"],
    [last + AHEAD, withYear(new Date(now.getFullYear(), now.getMonth() + AHEAD, 1))],
  ];

  return (
    <div ref={wrapRef} className="schart">
      {width > 0 && (
        <svg width={W} height={H} aria-hidden="true">
          <defs>
            <pattern id={hatch} width="4" height="4" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
              <rect width="4" height="4" style={{ fill: color, opacity: 0.35 }} />
              <line x1="0" y1="0" x2="0" y2="4" style={{ stroke: color, strokeWidth: 2 }} />
            </pattern>
          </defs>
          {ticks.map((t) => (
            <g key={t}>
              <line x1={pad.l} x2={W - pad.r} y1={y(t)} y2={y(t)} className="mchart-grid" />
              <text x={pad.l - 6} y={y(t)} dy="0.32em" textAnchor="end" className="mchart-tick">
                {compact(t)}
              </text>
            </g>
          ))}
          {/* The projection side sits on a faint panel of its own. */}
          <rect x={divider} y={pad.t} width={W - pad.r - divider} height={plotH} className="schart-future" />
          {s.cents.map((v, i) =>
            v > 0 ? (
              <path
                key={i}
                d={barPath(cx(i) - bw / 2, y(v), bw, plotH + pad.t - y(v))}
                style={{ fill: i === last ? `url(#${hatch})` : color }}
              />
            ) : null
          )}
          {s.pace_cents > 0 &&
            Array.from({ length: AHEAD }, (_, k) => (
              <path
                key={k}
                d={barPath(cx(n + k) - bw / 2, y(s.pace_cents), bw, plotH + pad.t - y(s.pace_cents))}
                style={{ fill: color, opacity: 0.22 }}
              />
            ))}
          {s.pace_cents > 0 && (
            <line x1={divider} x2={W - pad.r} y1={y(s.pace_cents)} y2={y(s.pace_cents)} className="schart-pace" />
          )}
          {s.kind === "rising" && s.baseline_cents > 0 && (
            <line x1={pad.l} x2={W - pad.r} y1={y(s.baseline_cents)} y2={y(s.baseline_cents)} className="mchart-avg" />
          )}
          <line x1={divider} x2={divider} y1={pad.t - 4} y2={pad.t + plotH} className="schart-divider" />
          {labels.map(([i, text]) => (
            <text
              key={i}
              {...(i === 0
                ? { x: pad.l, textAnchor: "start" as const }
                : i === total - 1
                  ? { x: W - pad.r, textAnchor: "end" as const }
                  : { x: cx(i), textAnchor: "middle" as const })}
              y={H - 5}
              className={`mchart-tick ${i === last ? "on" : ""}`}
            >
              {text}
            </text>
          ))}
        </svg>
      )}
      <div className="mlegend" aria-hidden="true">
        <span>
          <i className="key-bar" style={{ background: color }} />
          Spent
        </span>
        <span>
          <i className="key-bar" style={{ background: color, opacity: 0.3 }} />
          Next 12 months at {dollars(s.pace_cents)}/mo
        </span>
        {s.kind === "rising" && (
          <span>
            <i className="key-line avg" />
            Earlier level
          </span>
        )}
      </div>
      <table className="sr-only">
        <caption>Spending on {s.title} by month</caption>
        <tbody>
          {d.months.map((m, i) => (
            <tr key={m}>
              <th scope="row">
                {monthDate(m).toLocaleDateString(undefined, { month: "long", year: "numeric" })}
                {i === last ? " (so far)" : ""}
              </th>
              <td>{fmtCents(s.cents[i])}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SuggestionRow({
  s,
  d,
  onDismiss,
  busy,
  focused = false,
}: {
  s: Suggestion;
  d: Insights;
  onDismiss: (subjectType: "merchant" | "category", subjectKey: string, label: string) => void;
  busy: boolean;
  /** Opened from a nudge: expand it and bring it on screen. */
  focused?: boolean;
}) {
  const [open, setOpen] = useState(focused);
  const rowRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!focused) return;
    setOpen(true);
    rowRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [focused]);
  const [showTxns, setShowTxns] = useState(false);

  const txnParams = new URLSearchParams({
    direction: "expense",
    since: new Date(d.window_start).toISOString(),
  });
  if (s.subject_type === "merchant") txnParams.set("merchant", s.subject_key);
  else txnParams.set("category", s.subject_key);

  return (
    <div ref={rowRef} className={`srow ${open ? "open" : ""} ${focused ? "focused" : ""}`}>
      <button className="row series-toggle srow-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="icon-muted" style={{ lineHeight: 0 }}>
          {open ? <ChevronDownIcon size={16} /> : <ChevronRightIcon size={16} />}
        </span>
        <span className="grow" style={{ minWidth: 0 }}>
          <span className="row" style={{ gap: 7 }}>
            <span className="cat-dot" style={{ background: s.color || "var(--border)" }} aria-hidden="true" />
            <span className="txn-title">{s.title}</span>
          </span>
          <span className="muted small" style={{ display: "block" }}>
            {KIND_LABEL[s.kind]} · {summary(s)}
          </span>
        </span>
        {/* A need isn't a saving; show what it costs, not what cutting it would keep. */}
        {s.verdict === "essential" ? (
          <span className="ssave">
            <span className="txn-amount">{dollars(s.projected_cents)}</span>
            <span className="muted small">a year</span>
          </span>
        ) : (
          <span className="ssave">
            <span className="txn-amount pos">{dollars(s.savings_cents)}</span>
            <span className="muted small">saved a year</span>
          </span>
        )}
      </button>

      {open && (
        <div className="sbody">
          <Claim s={s} d={d} />
          {s.note && (
            <p className="snote">
              <SparkleIcon size={14} />
              <span>
                {s.note}
                <span className="snote-src"> · AI's read</span>
              </span>
            </p>
          )}

          <EvidenceChart s={s} d={d} />

          <div className="sstats">
            <div>
              <div className="muted small">Last 12 months</div>
              <div className="sstat">{fmtCents(s.last12_cents)}</div>
            </div>
            <div>
              <div className="muted small">Current pace</div>
              <div className="sstat">{dollars(s.pace_cents)}/mo</div>
            </div>
            <div>
              <div className="muted small">Next 12 months</div>
              <div className="sstat">{dollars(s.projected_cents)}</div>
            </div>
          </div>

          <button className="slink" onClick={() => setShowTxns(!showTxns)} aria-expanded={showTxns}>
            {showTxns ? "Hide transactions" : "Show the transactions behind this"}
          </button>
          {showTxns && <TxnList params={txnParams} indent={0} />}

          <div className="sactions">
            <button
              className="btn-secondary btn-small"
              disabled={busy}
              onClick={() => onDismiss(s.subject_type, s.subject_key, s.title)}
            >
              Not applicable
            </button>
            {s.subject_type === "merchant" && s.category_id && (
              <button
                className="btn-secondary btn-small"
                disabled={busy}
                onClick={() => onDismiss("category", s.category_id!, s.category_name)}
              >
                Never flag {s.category_name}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function Collapsible({
  title,
  count,
  children,
  initiallyOpen = false,
}: {
  title: string;
  count: number;
  children: React.ReactNode;
  initiallyOpen?: boolean;
}) {
  const [open, setOpen] = useState(initiallyOpen);
  if (count === 0) return null;
  return (
    <div className="sgroup">
      <button className="row series-toggle sgroup-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="icon-muted" style={{ lineHeight: 0 }}>
          {open ? <ChevronDownIcon size={15} /> : <ChevronRightIcon size={15} />}
        </span>
        <span className="grow muted">
          {title} ({count})
        </span>
      </button>
      {open && children}
    </div>
  );
}

/**
 * Spending worth a second look, and what cutting it would save.
 *
 * Fetched only when opened: the first look can involve an AI call, and most
 * visits to Cash flow aren't for this.
 */
export default function SavingsInsights({ focus }: { focus?: string | null }) {
  // Closed unless a nudge sent us here to see one suggestion.
  const [open, setOpen] = useState(!!focus);
  useEffect(() => {
    if (focus) setOpen(true);
  }, [focus]);
  const qc = useQueryClient();
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  const q = useQuery({
    queryKey: ["insights", tz],
    queryFn: () => get<Insights>(`/api/insights?${new URLSearchParams({ tz })}`),
    enabled: open,
    staleTime: 5 * 60_000,
    // While the AI is still at work, look again until its verdicts land.
    refetchInterval: (query) => (query.state.data?.judging ? 20_000 : false),
  });

  const dismiss = useMutation({
    mutationFn: (v: { subject_type: string; subject_key: string; label: string }) =>
      post("/api/insights/dismissals", v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["insights"] }),
  });
  const restore = useMutation({
    mutationFn: (v: InsightDismissal) =>
      del("/api/insights/dismissals", { subject_type: v.subject_type, subject_key: v.subject_key }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["insights"] }),
  });
  const busy = dismiss.isPending || restore.isPending;
  const onDismiss = (subject_type: "merchant" | "category", subject_key: string, label: string) =>
    dismiss.mutate({ subject_type, subject_key, label });

  const d = q.data;
  const worth = d?.suggestions.filter((s) => s.verdict !== "essential") ?? [];
  const needs = d?.suggestions.filter((s) => s.verdict === "essential") ?? [];
  // Merchant suggestions never overlap each other; a rising category can
  // contain them, so it stays out of the total rather than count twice.
  const potential = worth
    .filter((s) => s.subject_type === "merchant")
    .reduce((sum, s) => sum + s.savings_cents, 0);

  return (
    <>
      <div className="section-header">
        <h2>Ways to save</h2>
      </div>
      <div className="card scard">
        <button className="row series-toggle scard-head" onClick={() => setOpen(!open)} aria-expanded={open}>
          <span className="sicon" aria-hidden="true">
            <WalletIcon size={19} />
          </span>
          <span className="grow" style={{ minWidth: 0 }}>
            <span className="txn-title" style={{ display: "block" }}>
              {d && potential > 0 ? `Up to ${dollars(potential)} a year` : "Spending worth a second look"}
            </span>
            <span className="muted small" style={{ display: "block" }}>
              {d
                ? `${worth.length} suggestion${worth.length === 1 ? "" : "s"} from the last 12 months`
                : "Subscriptions and habits you could cut, with what that would save"}
            </span>
          </span>
          <span className="icon-muted" style={{ lineHeight: 0 }}>
            {open ? <ChevronDownIcon size={18} /> : <ChevronRightIcon size={18} />}
          </span>
        </button>

        {open && q.isLoading && (
          <div className="center" style={{ minHeight: 140, gap: 10 }}>
            <div className="spinner" />
            <p className="muted small">Going through the last 12 months. The first look can take up to a minute.</p>
          </div>
        )}
        {open && q.isError && <p className="error">Couldn't load suggestions.</p>}

        {open && d && (
          <div className="sbodywrap">
            {focus && !d.suggestions.some((s) => suggestionKey(s) === focus) && (
              <p className="muted small sgone">
                The suggestion that notification was about no longer applies. It may have been marked not
                applicable, or the spending behind it has changed.
              </p>
            )}
            {worth.map((s) => (
              <SuggestionRow
                key={suggestionKey(s)}
                s={s}
                d={d}
                onDismiss={onDismiss}
                busy={busy}
                focused={suggestionKey(s) === focus}
              />
            ))}
            {worth.length === 0 && (
              <p className="muted" style={{ padding: "10px 0" }}>
                Nothing stands out. No subscriptions, regular spending or rising categories worth flagging.
              </p>
            )}

            <Collapsible
              title="Looks necessary"
              count={needs.length}
              initiallyOpen={needs.some((s) => suggestionKey(s) === focus)}
            >
              <p className="muted small" style={{ margin: "0 0 4px 26px" }}>
                These look like needs, so they aren't counted above. Open one to check.
              </p>
              {needs.map((s) => (
                <SuggestionRow
                  key={suggestionKey(s)}
                  s={s}
                  d={d}
                  onDismiss={onDismiss}
                  busy={busy}
                  focused={suggestionKey(s) === focus}
                />
              ))}
            </Collapsible>

            <Collapsible title="Marked not applicable" count={d.dismissed.length}>
              {d.dismissed.map((x) => (
                <div key={suggestionKey(x)} className="row spread sdismissed">
                  <span className="grow">
                    <span className="txn-title">{x.label}</span>
                    <span className="muted small" style={{ display: "block" }}>
                      {x.subject_type === "category" ? "Whole category" : "Merchant"}
                    </span>
                  </span>
                  <button className="btn-secondary btn-small" disabled={busy} onClick={() => restore.mutate(x)}>
                    Flag again
                  </button>
                </div>
              ))}
            </Collapsible>

            {d.judging && (
              <p className="muted small sfoot">
                AI notes are still being written and will appear here shortly. Until then, simple rules decide
                what looks necessary.
              </p>
            )}
            <p className="muted small sfoot">
              Figures come straight from your transactions.{" "}
              {d.ai_enabled
                ? "AI only judges which spending looks necessary and writes the short notes."
                : "Which spending looks necessary is decided by simple rules; set ANTHROPIC_API_KEY for AI judgement and notes."}
            </p>
          </div>
        )}
      </div>
    </>
  );
}
