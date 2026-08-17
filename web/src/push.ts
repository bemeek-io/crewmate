// Web Push lifecycle, iOS-compliant:
//  - the VAPID public key is prefetched at page load so the opt-in click
//    handler can call Notification.requestPermission() synchronously
//  - the subscription is re-POSTed on EVERY launch (iOS drops them silently)
//  - push only works when installed via Share -> Add to Home Screen on iOS
import { get, post } from "./api/client";

let vapidKey: string | null = null;

export function pushSupported(): boolean {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

export function isStandalone(): boolean {
  return (
    window.matchMedia("(display-mode: standalone)").matches ||
    (navigator as unknown as { standalone?: boolean }).standalone === true
  );
}

export function isIOS(): boolean {
  return /iphone|ipad|ipod/i.test(navigator.userAgent);
}

/** iOS requires Add to Home Screen before push works. */
export function needsInstallForPush(): boolean {
  return isIOS() && !isStandalone();
}

export async function prefetchVapidKey(): Promise<void> {
  if (vapidKey || !pushSupported()) return;
  try {
    const res = await get<{ public_key: string }>("/api/push/vapid-public-key");
    vapidKey = res.public_key;
  } catch {
    /* retried on demand */
  }
}

function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const b64 = (base64 + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function saveSubscription(sub: PushSubscription): Promise<void> {
  const json = sub.toJSON();
  await post("/api/push/subscriptions", {
    endpoint: json.endpoint,
    keys: { p256dh: json.keys?.p256dh, auth: json.keys?.auth },
  });
}

/**
 * Must be invoked from a user-gesture click handler; requestPermission is
 * called synchronously within it.
 */
export async function enablePush(): Promise<"granted" | "denied" | "unsupported"> {
  if (!pushSupported() || !vapidKey) {
    await prefetchVapidKey();
    if (!vapidKey) return "unsupported";
  }
  const permission = await Notification.requestPermission();
  if (permission !== "granted") return "denied";
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(vapidKey!) as BufferSource,
  });
  await saveSubscription(sub);
  return "granted";
}

/** Called on every authenticated launch: re-register and re-POST. */
export async function syncPushSubscription(): Promise<void> {
  if (!pushSupported() || Notification.permission !== "granted") return;
  try {
    await prefetchVapidKey();
    const reg = await navigator.serviceWorker.ready;
    let sub = await reg.pushManager.getSubscription();
    if (!sub && vapidKey) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapidKey) as BufferSource,
      });
    }
    if (sub) await saveSubscription(sub);
  } catch {
    /* best effort — retried next launch */
  }
}

export async function registerServiceWorker(): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  try {
    await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch {
    /* offline shell + push unavailable, app still works */
  }
}
