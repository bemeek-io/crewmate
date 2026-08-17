/* Crewmate service worker: web push + minimal offline shell.
 * iOS rules honored here:
 *  - push handler ALWAYS calls showNotification (silent pushes are forbidden)
 *  - notificationclick focuses an existing window or opens a new one at data.url
 */

const CACHE = "crewmate-v1";

self.addEventListener("install", (e) => {
  self.skipWaiting();
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(["/"])));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET" || url.origin !== location.origin) return;
  if (url.pathname.startsWith("/api/")) {
    // Network only for the API — never serve stale money data.
    return;
  }
  if (url.pathname.startsWith("/assets/")) {
    // Hashed assets: cache-first.
    e.respondWith(
      caches.match(e.request).then(
        (hit) =>
          hit ||
          fetch(e.request).then((res) => {
            const copy = res.clone();
            caches.open(CACHE).then((c) => c.put(e.request, copy));
            return res;
          })
      )
    );
    return;
  }
  // App shell: network-first, cached fallback for offline launches.
  e.respondWith(
    fetch(e.request)
      .then((res) => {
        if (res.ok && (url.pathname === "/" || url.pathname === "/index.html")) {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put("/", copy));
        }
        return res;
      })
      .catch(() => caches.match("/"))
  );
});

self.addEventListener("push", (e) => {
  let data = { title: "Crewmate", body: "", url: "/" };
  try {
    if (e.data) data = { ...data, ...e.data.json() };
  } catch {
    /* keep defaults */
  }
  // ALWAYS show a notification — iOS revokes push permission for silent pushes.
  e.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body,
      icon: "/icons/icon-192.png",
      badge: "/icons/icon-192.png",
      data: { url: data.url },
    })
  );
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const url = e.notification.data?.url || "/";
  e.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((list) => {
      const win = list.find((c) => "focus" in c);
      if (win) {
        return win.focus().then((w) => ("navigate" in w ? w.navigate(url) : undefined));
      }
      return self.clients.openWindow(url);
    })
  );
});
