import type { Me } from "./types";

// The watcher polls Crew every minute, so an hour without a completed poll is
// sixty missed ones — far past anything a slow tick or a lease handover
// explains, and still short enough to catch the same day it starts.
const STALE_AFTER_MS = 60 * 60 * 1000;

export interface SyncHealth {
  /** Sync is meaningfully behind and the user should be told. */
  stale: boolean;
  /** Connected, but no poll has ever completed — starting up, not broken. */
  awaitingFirst: boolean;
  /** "3 minutes ago", or null when nothing has synced yet. */
  lastSyncedLabel: string | null;
}

// syncHealth judges whether transactions are actually flowing.
//
// Only a live connection is judged. A disconnected or expired one already says
// so in its own words, and a staleness warning on top would be noise pointing
// at the same fix.
export function syncHealth(me: Me | undefined): SyncHealth {
  const healthy = { stale: false, awaitingFirst: false, lastSyncedLabel: null };
  if (!me || me.crew_status !== "active") return healthy;

  if (!me.last_polled_at) return { ...healthy, awaitingFirst: true };
  const at = new Date(me.last_polled_at).getTime();
  if (Number.isNaN(at)) return healthy;

  const age = Date.now() - at;
  return {
    stale: age > STALE_AFTER_MS,
    awaitingFirst: false,
    lastSyncedLabel: relativeTime(age),
  };
}

// relativeTime renders an age coarsely: past a few minutes the exact figure
// stops mattering, and rounding down never overstates how fresh data is.
function relativeTime(ms: number): string {
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return "just now";
  if (mins === 1) return "1 minute ago";
  if (mins < 60) return `${mins} minutes ago`;
  const hours = Math.floor(mins / 60);
  if (hours === 1) return "1 hour ago";
  if (hours < 24) return `${hours} hours ago`;
  const days = Math.floor(hours / 24);
  return days === 1 ? "1 day ago" : `${days} days ago`;
}
