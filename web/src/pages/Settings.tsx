import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import type { Me } from "../api/types";
import { enablePush, needsInstallForPush, pushSupported } from "../push";
import { BellIcon, ShareIcon } from "../components/Icons";

export default function Settings() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
  const [perm, setPerm] = useState<NotificationPermission | "unsupported">("default");
  const [busy, setBusy] = useState(false);
  const [testSent, setTestSent] = useState(false);

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

  async function onTest() {
    await post("/api/push/test");
    setTestSent(true);
    setTimeout(() => setTestSent(false), 4000);
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
              {testSent ? "Sent — check your notifications" : "Send test notification"}
            </button>
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
                : crewStatus}
          </span>
        </div>
        {crewStatus === "needs_relogin" && (
          <Link to="/login">
            <button style={{ marginBottom: 10 }}>Reconnect Crew account</button>
          </Link>
        )}
        {crewStatus === "active" && (
          <button className="btn-danger" onClick={onDisconnect}>
            Disconnect Crew account
          </button>
        )}
      </div>

      <h2>Family</h2>
      <div className="card">
        <Link to="/family">
          <button className="btn-secondary">Manage family & invites</button>
        </Link>
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
