import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post, ApiError } from "../api/client";

type Step = "phone" | "sms" | "email";

export default function Login() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [step, setStep] = useState<Step>("phone");
  const [phone, setPhone] = useState("");
  const [code, setCode] = useState("");
  const [loginID, setLoginID] = useState("");
  const [emailMasked, setEmailMasked] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const fail = (e: unknown) =>
    setError(e instanceof ApiError ? e.message : "Something went wrong, try again");

  async function submitPhone(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await post<{ login_id: string }>("/api/auth/sms", { phone: phone.trim() });
      setLoginID(res.login_id);
      setCode("");
      setStep("sms");
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  }

  async function finishLogin(res: { has_family?: boolean }) {
    await qc.invalidateQueries();
    navigate(res.has_family ? "/" : "/onboarding", { replace: true });
  }

  async function submitSMS(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await post<{
        needs_email?: boolean;
        email_masked?: string;
        has_family?: boolean;
      }>("/api/auth/sms/verify", { login_id: loginID, code: code.trim() });
      if (res.needs_email) {
        setEmailMasked(res.email_masked ?? "");
        setCode("");
        setStep("email");
      } else {
        await finishLogin(res);
      }
    } catch (err) {
      fail(err);
      if (err instanceof ApiError && err.code === "login_expired") setStep("phone");
    } finally {
      setBusy(false);
    }
  }

  async function submitEmail(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await post<{ has_family?: boolean }>("/api/auth/email/verify", {
        login_id: loginID,
        code: code.trim(),
      });
      await finishLogin(res);
    } catch (err) {
      fail(err);
      if (err instanceof ApiError && err.code === "login_expired") setStep("phone");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="center">
      <div style={{ fontSize: "3rem" }}>💳</div>
      <h1>Crewmate</h1>
      <p className="muted">Sign in with your Crew account. Your credentials go straight to Crew — Crewmate never stores your phone number or codes.</p>

      {step === "phone" && (
        <form onSubmit={submitPhone} style={{ width: "100%" }}>
          <input
            type="tel"
            inputMode="tel"
            autoComplete="tel"
            placeholder="Phone number"
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            required
          />
          {error && <div className="error">{error}</div>}
          <button disabled={busy || !phone.trim()}>{busy ? "Sending…" : "Send code"}</button>
        </form>
      )}

      {step === "sms" && (
        <form onSubmit={submitSMS} style={{ width: "100%" }}>
          <p className="muted">Enter the code we texted you.</p>
          <input
            className="otp-input"
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="••••••"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            required
          />
          {error && <div className="error">{error}</div>}
          <button disabled={busy || !code.trim()}>{busy ? "Verifying…" : "Verify"}</button>
        </form>
      )}

      {step === "email" && (
        <form onSubmit={submitEmail} style={{ width: "100%" }}>
          <p className="muted">One more step — enter the code sent to {emailMasked || "your email"}.</p>
          <input
            className="otp-input"
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="••••••"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            required
          />
          {error && <div className="error">{error}</div>}
          <button disabled={busy || !code.trim()}>{busy ? "Verifying…" : "Verify"}</button>
        </form>
      )}
    </div>
  );
}
