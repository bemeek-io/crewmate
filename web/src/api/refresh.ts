import { useEffect } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";

/** Everything that changes when a transaction's category changes. */
const CATEGORY_AFFECTED = [["transactions"], ["cashflow"], ["recurring"], ["rules"]];

/**
 * Categorizing is not immediate: the category is written to Crew's note field
 * by whichever replica holds that connection's lease, and only mirrored back
 * locally once that write lands. Invalidating on mutation success therefore
 * re-reads data that hasn't changed yet — which is why a filtered list kept
 * showing a transaction that had already been categorized.
 *
 * So re-check a few times across the window the write realistically takes,
 * rather than once, immediately, too early.
 */
const SETTLE_DELAYS_MS = [0, 2_000, 6_000, 15_000];

export function refreshAfterCategorize(qc: QueryClient): () => void {
  const timers = SETTLE_DELAYS_MS.map((delay) =>
    setTimeout(() => {
      for (const key of CATEGORY_AFFECTED) {
        qc.invalidateQueries({ queryKey: key });
      }
    }, delay)
  );
  return () => timers.forEach(clearTimeout);
}

export function useRefreshAfterCategorize(): () => void {
  const qc = useQueryClient();
  return () => {
    refreshAfterCategorize(qc);
  };
}

/**
 * Refetch when the app comes back to the foreground.
 *
 * react-query's refetchOnWindowFocus covers the desktop case, but an installed
 * iOS web app is resumed rather than focused and doesn't reliably fire it —
 * which is what made closing and reopening the only way to see server-side
 * categorization land.
 */
export function useRefreshOnResume(): void {
  const qc = useQueryClient();
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === "visible") {
        qc.invalidateQueries();
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [qc]);
}
