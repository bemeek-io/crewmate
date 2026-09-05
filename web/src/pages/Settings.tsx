import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import type { Me } from "../api/types";
import { syncHealth } from "../api/sync";
import { enablePush, needsInstallForPush, pushSupported } from "../push";
import { BellIcon, ShareIcon } from "../components/Icons";

export default function Settings() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
  const [perm, setPerm] = useState<NotificationPermission | "unsupported">("default");
  const [busy, setBusy] = useState(false);
  const [sentOK, setSentOK] = useState(false);
  const [diagnosis, setDiagnosis] = useState("");

  useEffect(() => {
    setPerm(pushSupported() ? Notification.permission : "unsupported");
  }, []);

  // NOTE: enablePush calls Notification.requestPermission() synchronously in
  // this click handler — required by iOS.
  async function onEnable() {
    setBusy(true);
    const result = await enablePush();
    setBusy(false);
    if (result === "granted") setPerm("granted");
    else if (result === "denied") setPerm("denied");
  }

  // A silent phone has three distinct causes and they need different fixes, so
  // say which one it is rather than reporting a cheerful "sent".
  async function onTest() {
    setDiagnosis("");
    try {
      const res = await post<{ devices?: number; accepted?: number; problem?: string }>(
        "/api/push/test"
      );
      if (!res.devices) {
        setDiagnosis(
          "This device isn't registered for notifications, so nothing was sent. On iPhone, " +
            "push only works when the app is added to the Home Screen from Safari — an icon " +
            "created by Chrome never registers. Reinstall from Safari, open it from the new " +
            "icon, and turn notifications on there."
        );
      } else if (res.problem) {
        setDiagnosis(`The push service rejected it — ${res.problem}`);
      } else if (!res.accepted) {
        setDiagnosis("Accepted by no device. Turn notifications off and on again to re-register.");
      } else {
        setSentOK(true);
        setTimeout(() => setSentOK(false), 4000);
      }
    } catch (e) {
      setDiagnosis(e instanceof Error ? e.message : "Could not reach the server.");
    }
  }

  async function onLogout() {
    await post("/api/auth/logout");
    qc.clear();
    navigate("/login", { replace: true });
  }

  async function onDisconnect() {
    if (
      !confirm(
        "Disconnect your Crew account? Crewmate stops tracking your transactions until you sign in again."
      )
    )
      return;
    await del("/api/crew/connection");
    await qc.invalidateQueries({ queryKey: ["me"] });
  }

  const crewStatus = me.data?.crew_status ?? "none";
  const sync = syncHealth(me.data);

  return (
    <>
      <h1>Settings</h1>

      <h2>Notifications</h2>
      <div className="card">
        {needsInstallForPush() ? (
          <>
            <p className="icon-heading">
              <ShareIcon size={18} className="icon-muted" /> Install Crewmate first
            </p>
            <p className="muted small">
              On iPhone, notifications only work when Crewmate is installed as an app: open the{" "}
              <b>Share</b> menu in Safari, choose <b>Add to Home Screen</b>, then launch Crewmate
              from the icon and enable notifications here.
            </p>
          </>
        ) : perm === "unsupported" ? (
          <p className="muted">This browser doesn’t support push notifications.</p>
        ) : perm === "granted" ? (
          <>
            <p className="icon-heading">
              <BellIcon size={18} className="icon-muted" /> Notifications are on
            </p>
            <button className="btn-secondary" onClick={onTest}>
              {sentOK ? "Sent — check your notifications" : "Send test notification"}
            </button>
            {diagnosis && (
              <p className="muted small" style={{ marginTop: 10 }}>
                {diagnosis}
              </p>
            )}
          </>
        ) : perm === "denied" ? (
          <p className="muted">
            Notifications are blocked. Enable them for Crewmate in your device settings.
          </p>
        ) : (
          <>
            <p className="muted small" style={{ marginBottom: 10 }}>
              Get a push the moment a new transaction lands — auto-categorized, or with a one-tap
              prompt to file it.
            </p>
            <button onClick={onEnable} disabled={busy}>
              {busy ? "Enabling…" : "Enable notifications"}
            </button>
          </>
        )}
      </div>

      <h2>Crew account</h2>
      <div className="card">
        <div className="row spread" style={{ marginBottom: 10 }}>
          <span>Status</span>
          <span
            className={`pill ${crewStatus === "active" ? "accent" : crewStatus === "needs_relogin" ? "warn" : ""}`}
          >
            {crewStatus === "active"
              ? "connected"
              : crewStatus === "needs_relogin"
                ? "reconnect needed"
                : crewStatus === "disabled"
                  ? "disconnected"
                  : "not connected"}
          </span>
        </div>
        {/* Status says whether Crew still takes our token; this says whether
            transactions are actually arriving. They can disagree for days. */}
        {crewStatus === "active" && (
          <div className="row spread" style={{ marginBottom: 10 }}>
            <span>Last synced</span>
            <span className={sync.stale ? "pill warn" : "muted small"}>
              {sync.awaitingFirst ? "waiting for first sync…" : sync.lastSyncedLabel}
            </span>
          </div>
        )}
        {sync.stale && (
          <p className="muted small" style={{ marginBottom: 10 }}>
            Crewmate hasn’t synced with Crew in over an hour, so new transactions may be
            missing. It retries on its own — if this doesn’t clear, the server logs will say
            why.
          </p>
        )}
        {/* Every state that isn't connected needs a way back: signing in again
            re-activates the connection and back-fills whatever was missed
            while it was off. Offering this only for needs_relogin left a
            disconnect with no way to undo it. */}
        {crewStatus === "active" ? (
          <button className="btn-danger" onClick={onDisconnect}>
            Disconnect Crew account
          </button>
        ) : (
          <>
            <Link to="/login">
              <button style={{ marginBottom: 10 }}>
                {crewStatus === "none" ? "Connect Crew account" : "Reconnect Crew account"}
              </button>
            </Link>
            <p className="muted small">
              {crewStatus === "needs_relogin"
                ? "Crewmate lost access to your Crew account. Sign in again to keep tracking transactions."
                : "Sign in again to resume tracking. Transactions that landed while you were disconnected are picked up automatically."}
            </p>
          </>
        )}
      </div>

      <div className="card">
        <button className="btn-danger" onClick={onLogout}>
          Sign out
        </button>
      </div>

      <p className="muted small" style={{ textAlign: "center" }}>
        Crewmate never stores your phone number, OTP codes, or shows your Crew credentials to
        anyone. Your Crew session token is encrypted at rest.
      </p>
    </>
  );
}
