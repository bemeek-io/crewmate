import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { get, fmtCents } from "../api/client";
import type { MonthlyCategory, MonthlySeries, MonthlySpend as Data } from "../api/types";
import { CloseIcon } from "./Icons";

const WINDOWS = [
  { key: "6", label: "6M" },
  { key: "12", label: "1Y" },
];

/** Categories shown before "Show all" — the long tail is rarely the story. */
const TOP_CATEGORIES = 8;

/** Before this many days a month's pace is mostly noise. */
const MIN_PACE_DAYS = 5;

/** Within this share of the average, a month reads as "on average". */
const STEADY = 0.05;

const monthDate = (key: string) => {
  const [y, m] = key.split("-").map(Number);
  return new Date(y, m - 1, 1);
};
const monthShort = (key: string) => monthDate(key).toLocaleDateString(undefined, { month: "short" });
const monthLong = (key: string, withYear = false) =>
  monthDate(key).toLocaleDateString(undefined, withYear ? { month: "long", year: "numeric" } : { month: "long" });

/** $1,240 → "$1.2k": axis ticks only; every exact figure uses fmtCents. */
function compact(cents: number): string {
  const d = cents / 100;
  if (d >= 1000) {
    const k = d / 1000;
    return `$${k >= 10 || Number.isInteger(k) ? Math.round(k) : k.toFixed(1)}k`;
  }
  return `$${Math.round(d)}`;
}

/** Whole dollars — a trend of $41.87/mo claims more precision than a fit has. */
const dollars = (cents: number) =>
  (Math.abs(cents) / 100).toLocaleString(undefined, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  });

/** Round axis ticks from 0 to at least max, three or four steps. */
function niceTicks(max: number): number[] {
  if (max <= 0) return [0];
  const raw = max / 3;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((f) => f * mag).find((s) => s >= raw) ?? raw;
  const ticks: number[] = [];
  for (let v = 0; v < max + step * 0.999; v += step) ticks.push(v);
  return ticks;
}

/** A column with a rounded data end and a square foot on the baseline. */
function barPath(x: number, y: number, w: number, h: number) {
  const r = Math.min(4, w / 2, h);
  const b = y + h;
  return `M${x},${b}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${b}Z`;
}

type Delta = { pct: number; label: string; tone: "up" | "down" | "flat" };

/** How a month compares with the average. Spending more is the bad direction. */
function delta(value: number, avg: number): Delta | null {
  if (avg <= 0) return null;
  const pct = (value - avg) / avg;
  if (Math.abs(pct) < STEADY) return { pct, label: "Right on average", tone: "flat" };
  const p = Math.round(Math.abs(pct) * 100);
  return pct > 0
    ? { pct, label: `${p}% above average`, tone: "up" }
    : { pct, label: `${p}% below average`, tone: "down" };
}

function DeltaText({ d, short = false }: { d: Delta; short?: boolean }) {
  const arrow = d.tone === "up" ? "▲" : d.tone === "down" ? "▼" : "●";
  const text = short
    ? d.tone === "flat"
      ? "avg"
      : `${Math.round(Math.abs(d.pct) * 100)}%`
    : d.label;
  return (
    <span className={`mdelta ${d.tone}`}>
      <span aria-hidden="true" className="mdelta-arrow">
        {arrow}
      </span>
      {text}
    </span>
  );
}

/** Tracks an element's width so the chart draws at real pixels, not a
 *  stretched viewBox that would squash its text. */
function useWidth<T extends HTMLElement>(): [React.RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    const ro = new ResizeObserver(([e]) => setWidth(e.contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

function Chart({
  data,
  series,
  color,
  active,
  onActive,
}: {
  data: Data;
  series: MonthlySeries;
  color: string;
  active: number;
  onActive: (i: number) => void;
}) {
  const [wrapRef, width] = useWidth<HTMLDivElement>();
  const hatch = `hatch-${useId().replace(/:/g, "")}`;
  const n = data.months.length;
  const last = n - 1;
  const hs = data.history_start;
  const showPace = data.days_elapsed >= MIN_PACE_DAYS;

  const W = width || 320;
  const H = 188;
  const pad = { l: 38, r: 4, t: 10, b: 24 };
  const plotW = W - pad.l - pad.r;
  const plotH = H - pad.t - pad.b;

  const trendAt = (i: number) =>
    series.trend ? Math.max(0, series.trend.intercept_cents + series.trend.slope_cents * i) : 0;
  const peak = Math.max(
    ...series.cents,
    showPace ? series.projected_cents : 0,
    series.avg_cents,
    series.trend ? Math.max(trendAt(hs), trendAt(last)) : 0
  );
  const ticks = niceTicks(peak);
  const top = ticks[ticks.length - 1] || 1;
  const y = (v: number) => pad.t + plotH - (v / top) * plotH;

  const slot = plotW / n;
  const bw = Math.min(24, slot * 0.58);
  const cx = (i: number) => pad.l + slot * (i + 0.5);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowLeft") onActive(Math.max(hs, active - 1));
    else if (e.key === "ArrowRight") onActive(Math.min(last, active + 1));
    else return;
    e.preventDefault();
  };

  return (
    <div ref={wrapRef} className="mchart" tabIndex={0} onKeyDown={onKey} aria-label="Monthly spending chart. Use the arrow keys to move between months.">
      {width > 0 && (
        <svg width={W} height={H} role="img" aria-hidden="true">
          <defs>
            {/* The month in progress is textured, not just faded, so "partial"
                doesn't rely on reading a lighter shade. */}
            <pattern id={hatch} width="5" height="5" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
              <rect width="5" height="5" style={{ fill: color, opacity: 0.35 }} />
              <line x1="0" y1="0" x2="0" y2="5" style={{ stroke: color, strokeWidth: 2.5 }} />
            </pattern>
          </defs>

          {/* Selected month */}
          {active >= hs && (
            <rect
              x={pad.l + slot * active + 2}
              y={pad.t - 6}
              width={slot - 4}
              height={plotH + 6}
              rx={7}
              className="mchart-focus"
            />
          )}

          {ticks.map((t) => (
            <g key={t}>
              <line x1={pad.l} x2={W - pad.r} y1={y(t)} y2={y(t)} className="mchart-grid" />
              <text x={pad.l - 7} y={y(t)} dy="0.32em" textAnchor="end" className="mchart-tick">
                {compact(t)}
              </text>
            </g>
          ))}

          {data.months.map((m, i) => {
            if (i < hs) return null;
            const v = series.cents[i];
            const x = cx(i) - bw / 2;
            const inProgress = i === last;
            return (
              <g key={m}>
                {inProgress && showPace && series.projected_cents > v && (
                  <path
                    d={barPath(x, y(series.projected_cents), bw, plotH + pad.t - y(series.projected_cents))}
                    style={{ fill: color, opacity: 0.12 }}
                  />
                )}
                {v > 0 && (
                  <path
                    d={barPath(x, y(v), bw, plotH + pad.t - y(v))}
                    style={{ fill: inProgress ? `url(#${hatch})` : color }}
                  />
                )}
              </g>
            );
          })}

          {series.avg_cents > 0 && (
            <line
              x1={pad.l}
              x2={W - pad.r}
              y1={y(series.avg_cents)}
              y2={y(series.avg_cents)}
              className="mchart-avg"
            />
          )}

          {series.trend && (
            <>
              <line
                x1={cx(hs)}
                x2={cx(last - 1)}
                y1={y(trendAt(hs))}
                y2={y(trendAt(last - 1))}
                className="mchart-trend"
              />
              {/* Into the month in progress it's a forecast, so it's dashed. */}
              <line
                x1={cx(last - 1)}
                x2={cx(last)}
                y1={y(trendAt(last - 1))}
                y2={y(trendAt(last))}
                className="mchart-trend forecast"
              />
            </>
          )}

          {data.months.map((m, i) => (
            <text
              key={m}
              x={cx(i)}
              y={H - 6}
              textAnchor="middle"
              className={`mchart-tick ${i === active ? "on" : ""}`}
            >
              {/* Sep '26 on a year boundary, so a 1Y view is unambiguous. */}
              {slot < 26 && i % 2 !== last % 2 && i !== active
                ? ""
                : monthShort(m) + (monthDate(m).getMonth() === 0 && n > 6 ? ` ’${String(monthDate(m).getFullYear()).slice(2)}` : "")}
            </text>
          ))}

          {/* Hit targets: the whole column, not the bar, so short months and
              the empty space above them are just as easy to tap. */}
          {data.months.map((m, i) =>
            i < hs ? null : (
              <rect
                key={m}
                x={pad.l + slot * i}
                y={0}
                width={slot}
                height={H}
                fill="transparent"
                style={{ cursor: "pointer" }}
                onPointerEnter={(e) => e.pointerType === "mouse" && onActive(i)}
                onClick={() => onActive(i)}
              />
            )
          )}
        </svg>
      )}
    </div>
  );
}

function Legend({ color, series }: { color: string; series: MonthlySeries }) {
  return (
    <div className="mlegend" aria-hidden="true">
      <span>
        <i className="key-bar" style={{ background: color }} />
        Spent
      </span>
      <span>
        <i className="key-bar key-hatch" style={{ color }} />
        This month
      </span>
      {series.avg_cents > 0 && (
        <span>
          <i className="key-line avg" />
          Average
        </span>
      )}
      {series.trend && (
        <span>
          <i className="key-line trend" />
          Trend
        </span>
      )}
    </div>
  );
}

/** Per-category monthly columns, drawn small enough to sit in a list row. */
function Spark({ cat, data, active }: { cat: MonthlyCategory; data: Data; active: number }) {
  const n = data.months.length;
  const w = 64;
  const h = 22;
  const gap = n > 8 ? 1.5 : 2;
  const bw = (w - gap * (n - 1)) / n;
  const max = Math.max(...cat.cents, 1);
  const color = cat.color || "var(--muted)";
  return (
    <svg width={w} height={h} aria-hidden="true" className="mspark">
      {cat.cents.map((v, i) => {
        if (i < data.history_start) return null;
        const bh = Math.max(v > 0 ? 2 : 1, (v / max) * h);
        return (
          <rect
            key={i}
            x={i * (bw + gap)}
            y={h - bh}
            width={bw}
            height={bh}
            rx={Math.min(1.5, bw / 2)}
            style={{
              fill: v > 0 ? color : "var(--border)",
              opacity: i === active ? 1 : 0.4,
            }}
          />
        );
      })}
    </svg>
  );
}

const catKey = (c: MonthlyCategory) => c.category_id ?? "misc";
const catName = (c: MonthlyCategory) => c.category_name || "Uncategorized";

/**
 * Spending by calendar month: each month's total against the average of the
 * complete months, with a trend line through them — overall, or for one
 * category picked from the list beneath.
 *
 * The month in progress is shown for what it is: what's been spent so far,
 * with its projected pace drawn faintly behind it, and it's kept out of the
 * average, which a half-finished month would only drag down.
 */
export default function MonthlySpending() {
  const [months, setMonths] = useState("6");
  const [selected, setSelected] = useState<string | null>(null);
  const [active, setActive] = useState<number | null>(null);
  const [showAll, setShowAll] = useState(false);
  const chartCard = useRef<HTMLDivElement>(null);
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  const q = useQuery({
    queryKey: ["cashflow", "monthly", months, tz],
    queryFn: () =>
      get<Data>(`/api/cashflow/monthly?${new URLSearchParams({ months, tz })}`),
  });
  const d = q.data;

  // A new window re-indexes the months, so the selection goes back to now.
  useEffect(() => setActive(null), [months]);

  const cat = d?.categories.find((c) => catKey(c) === selected) ?? null;
  const series = cat ?? d?.total;
  const color = cat ? cat.color || "var(--muted)" : "var(--accent)";

  const select = (key: string | null) => {
    setSelected(key);
    // The chart is what changes; bring it back on screen to see it happen.
    const el = chartCard.current;
    if (key && el && el.getBoundingClientRect().top < 0) {
      el.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };

  return (
    <>
      <div className="section-header">
        <h2>Monthly spending</h2>
        <div className="chips" style={{ margin: 0, gap: 6 }}>
          {WINDOWS.map((w) => (
            <button
              key={w.key}
              className={`chip sm ${months === w.key ? "on" : ""}`}
              onClick={() => setMonths(w.key)}
            >
              {w.label}
            </button>
          ))}
        </div>
      </div>

      {q.isLoading && (
        <div className="card center" style={{ minHeight: 280 }}>
          <div className="spinner" />
        </div>
      )}
      {q.isError && <p className="error">Couldn't load monthly spending.</p>}

      {d && series && (
        <MonthlyBody
          d={d}
          cat={cat}
          series={series}
          color={color}
          active={active ?? d.months.length - 1}
          onActive={setActive}
          select={select}
          selected={selected}
          showAll={showAll}
          setShowAll={setShowAll}
          chartCard={chartCard}
        />
      )}
    </>
  );
}

function MonthlyBody({
  d,
  cat,
  series,
  color,
  active,
  onActive,
  select,
  selected,
  showAll,
  setShowAll,
  chartCard,
}: {
  d: Data;
  cat: MonthlyCategory | null;
  series: MonthlySeries;
  color: string;
  active: number;
  onActive: (i: number) => void;
  select: (key: string | null) => void;
  selected: string | null;
  showAll: boolean;
  setShowAll: (v: boolean) => void;
  chartCard: React.RefObject<HTMLDivElement>;
}) {
  const last = d.months.length - 1;
  const inProgress = active === last;
  const paced = inProgress && d.days_elapsed >= MIN_PACE_DAYS;
  const value = series.cents[active];
  const compareTo = paced ? series.projected_cents : value;
  const vsAvg = inProgress && !paced ? null : delta(compareTo, series.avg_cents);
  const monthsOfHistory = last - d.history_start;

  const slope = series.trend?.slope_cents ?? 0;
  // Steady means the fit barely moves across the whole history — judged per
  // month, a real $80/mo climb would pass for flat against a $2,400 average.
  const steady =
    series.avg_cents > 0 &&
    Math.abs(slope * Math.max(1, monthsOfHistory - 1)) < series.avg_cents * STEADY;

  const cats = showAll ? d.categories : d.categories.slice(0, TOP_CATEGORIES);

  return (
    <>
      <div className="card mcard" ref={chartCard}>
        {cat && (
          <button
            className="chip sm mfilter"
            onClick={() => select(null)}
            aria-label={`Showing ${catName(cat)}. Show all categories`}
          >
            <span className="cat-dot" style={{ background: color }} aria-hidden="true" />
            {catName(cat)}
            <CloseIcon size={13} />
          </button>
        )}

        <div className="muted small">
          {inProgress ? `${monthLong(d.months[active])} so far` : monthLong(d.months[active], true)}
        </div>
        <div className="mhero">{fmtCents(value)}</div>
        <div className="small mhero-sub">
          {paced && <span className="muted">On pace for {dollars(series.projected_cents)} · </span>}
          {inProgress && !paced && (
            <span className="muted">
              Day {d.days_elapsed} of {d.days_in_month}
            </span>
          )}
          {vsAvg && <DeltaText d={vsAvg} />}
          {!inProgress && !vsAvg && <span className="muted">Not enough history to compare yet</span>}
        </div>

        <Chart data={d} series={series} color={color} active={active} onActive={onActive} />
        <Legend color={color} series={series} />

        <div className="mstats">
          <div>
            <div className="muted small">Monthly average</div>
            <div className="mstat">{series.avg_cents > 0 ? dollars(series.avg_cents) : "—"}</div>
            <div className="muted small">
              {monthsOfHistory > 0
                ? `over ${monthsOfHistory} full month${monthsOfHistory === 1 ? "" : "s"}`
                : "after a full month"}
            </div>
          </div>
          <div>
            <div className="muted small">Trend</div>
            <div className="mstat">
              {!series.trend
                ? "—"
                : steady
                  ? "Steady"
                  : `${slope > 0 ? "+" : "−"}${dollars(slope)}`}
              {series.trend && !steady && <span className="muted small"> /mo</span>}
            </div>
            <div className="small">
              {!series.trend ? (
                <span className="muted">needs 3 full months</span>
              ) : steady ? (
                <span className="muted">little change over the period</span>
              ) : (
                <DeltaText
                  d={{
                    pct: slope,
                    tone: slope > 0 ? "up" : "down",
                    label: slope > 0 ? "Spending rising" : "Spending falling",
                  }}
                />
              )}
            </div>
          </div>
        </div>

        {/* The chart's numbers for screen readers — and the table view. */}
        <table className="sr-only">
          <caption>Monthly spending{cat ? ` on ${catName(cat)}` : ""}</caption>
          <tbody>
            {d.months.map((m, i) =>
              i < d.history_start ? null : (
                <tr key={m}>
                  <th scope="row">{monthLong(m, true)}{i === last ? " (so far)" : ""}</th>
                  <td>{fmtCents(series.cents[i])}</td>
                </tr>
              )
            )}
          </tbody>
        </table>
      </div>

      {d.categories.length > 0 && (
        <>
          <div className="section-header" style={{ marginTop: 18 }}>
            <h2>By category</h2>
            <span className="muted small">
              {inProgress ? `${monthShort(d.months[active])} so far` : monthLong(d.months[active], true)}
            </span>
          </div>
          <div className="card mcats">
            {cats.map((c) => {
              const key = catKey(c);
              const on = key === selected;
              const v = c.cents[active];
              const cd = inProgress
                ? paced
                  ? delta(c.projected_cents, c.avg_cents)
                  : null
                : delta(v, c.avg_cents);
              return (
                <div key={key} className={`mcat ${on ? "on" : ""}`}>
                  <button
                    className="row series-toggle mcat-btn"
                    onClick={() => select(on ? null : key)}
                    aria-pressed={on}
                  >
                    <span className="grow" style={{ minWidth: 0 }}>
                      <span className="row" style={{ gap: 7 }}>
                        <span
                          className="cat-dot"
                          style={{ background: c.color || "var(--border)" }}
                          aria-hidden="true"
                        />
                        <span className="txn-title">{catName(c)}</span>
                      </span>
                      <span className="muted small" style={{ display: "block" }}>
                        {c.avg_cents > 0 ? `${dollars(c.avg_cents)} avg / mo` : "No full months yet"}
                      </span>
                    </span>
                    <Spark cat={c} data={d} active={active} />
                    <span className="mcat-amt">
                      <span className="txn-amount">{fmtCents(v)}</span>
                      {cd ? (
                        <span className="small">
                          <DeltaText d={cd} short />
                        </span>
                      ) : (
                        <span className="small">&nbsp;</span>
                      )}
                    </span>
                  </button>
                  {on && (
                    <div className="mcat-months">
                      {d.months.map((m, i) =>
                        i < d.history_start ? null : (
                          <button
                            key={m}
                            className={`mcat-month ${i === active ? "on" : ""}`}
                            onClick={() => onActive(i)}
                          >
                            <span className="muted small">{monthShort(m)}</span>
                            <span className="txn-amount small">{dollars(c.cents[i])}</span>
                          </button>
                        )
                      )}
                    </div>
                  )}
                </div>
              );
            })}
            {d.categories.length > TOP_CATEGORIES && (
              <button className="mcat-more" onClick={() => setShowAll(!showAll)}>
                {showAll ? "Show fewer" : `Show all ${d.categories.length} categories`}
              </button>
            )}
          </div>
        </>
      )}
    </>
  );
}
