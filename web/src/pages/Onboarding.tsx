import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post, ApiError } from "../api/client";
import { UsersIcon } from "../components/Icons";

export default function Onboarding() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [mode, setMode] = useState<"choose" | "create" | "join">("choose");
  const [name, setName] = useState("");
  const [inviteCode, setInviteCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function done() {
    await qc.invalidateQueries();
    navigate("/", { replace: true });
  }

  async function createFamily(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await post("/api/family", { name: name.trim() });
      await done();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not create family");
    } finally {
      setBusy(false);
    }
  }

  async function joinFamily(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await post("/api/family/join", { code: inviteCode.trim().toUpperCase() });
      await done();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not join family");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="center">
      <UsersIcon size={44} className="icon-muted" />
      <h1>Set up your crew</h1>

      {mode === "choose" && (
        <div style={{ width: "100%", display: "flex", flexDirection: "column", gap: 10 }}>
          <p className="muted">
            Categories and transactions are shared with everyone in your family.
          </p>
          <button onClick={() => setMode("create")}>Create a family</button>
          <button className="btn-secondary" onClick={() => setMode("join")}>
            I have an invite code
          </button>
        </div>
      )}

      {mode === "create" && (
        <form onSubmit={createFamily} style={{ width: "100%" }}>
          <input
            placeholder="Family name (e.g. The Meekers)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
            required
          />
          {error && <div className="error">{error}</div>}
          <button disabled={busy || !name.trim()}>{busy ? "Creating…" : "Create"}</button>
          <p style={{ marginTop: 10 }}>
            <a onClick={() => setMode("choose")} style={{ cursor: "pointer" }}>
              Back
            </a>
          </p>
        </form>
      )}

      {mode === "join" && (
        <form onSubmit={joinFamily} style={{ width: "100%" }}>
          <input
            className="otp-input"
            placeholder="INVITE CODE"
            value={inviteCode}
            onChange={(e) => setInviteCode(e.target.value.toUpperCase())}
            maxLength={8}
            required
          />
          {error && <div className="error">{error}</div>}
          <button disabled={busy || !inviteCode.trim()}>{busy ? "Joining…" : "Join family"}</button>
          <p style={{ marginTop: 10 }}>
            <a onClick={() => setMode("choose")} style={{ cursor: "pointer" }}>
              Back
            </a>
          </p>
        </form>
      )}
    </div>
  );
}
