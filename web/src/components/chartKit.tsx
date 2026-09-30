import { useLayoutEffect, useRef, useState } from "react";

/* Pieces shared by the hand-drawn SVG charts. */

export const monthDate = (key: string) => {
  const [y, m] = key.split("-").map(Number);
  return new Date(y, m - 1, 1);
};
export const monthShort = (key: string) => monthDate(key).toLocaleDateString(undefined, { month: "short" });
/** $1,240 → "$1.2k": axis ticks only; every exact figure uses fmtCents. */
export function compact(cents: number): string {
  const d = cents / 100;
  if (d >= 1000) {
    const k = d / 1000;
    return `$${k >= 10 || Number.isInteger(k) ? Math.round(k) : k.toFixed(1)}k`;
  }
  return `$${Math.round(d)}`;
}

/** Whole dollars — a trend of $41.87/mo claims more precision than a fit has. */
export const dollars = (cents: number) =>
  (Math.abs(cents) / 100).toLocaleString(undefined, {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  });

/** Round axis ticks from 0 to at least max, three or four steps. */
export function niceTicks(max: number): number[] {
  if (max <= 0) return [0];
  const raw = max / 3;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((f) => f * mag).find((s) => s >= raw) ?? raw;
  const ticks: number[] = [];
  for (let v = 0; v < max + step * 0.999; v += step) ticks.push(v);
  return ticks;
}

/** A column with a rounded data end and a square foot on the baseline. */
export function barPath(x: number, y: number, w: number, h: number) {
  const r = Math.min(4, w / 2, h);
  const b = y + h;
  return `M${x},${b}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${b}Z`;
}

/** Tracks an element's width so the chart draws at real pixels, not a
 *  stretched viewBox that would squash its text. */
export function useWidth<T extends HTMLElement>(): [React.RefObject<T>, number] {
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

