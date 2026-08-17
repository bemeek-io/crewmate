import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import type { FamilyInfo, Me } from "../api/types";

export default function Family() {
  const qc = useQueryClient();
  const [invite, setInvite] = useState<{ code: string; expires_at: string } | null>(null);

  const me = useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me") });
  const family = useQuery({
    queryKey: ["family"],
    queryFn: () => get<FamilyInfo>("/api/family"),
  });

  const createInvite = useMutation({
    mutationFn: () => post<{ code: string; expires_at: string }>("/api/family/invites"),
    onSuccess: setInvite,
  });

  const removeMember = useMutation({
    mutationFn: (userID: string) => del(`/api/family/members/${userID}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["family"] }),
  });

  const isAdmin = family.data?.role === "admin";

  async function shareInvite() {
    if (!invite) return;
    const text = `Join our family on Crewmate! Invite code: ${invite.code}`;
    if (navigator.share) {
      try {
        await navigator.share({ text });
      } catch {
        /* canceled */
      }
    } else {
      await navigator.clipboard.writeText(text);
      alert("Copied to clipboard");
    }
  }

  return (
    <>
      <h1>{family.data?.name ?? "Family"}</h1>
      <div className="card">
        {(family.data?.members ?? []).map((m) => (
          <div className="row spread" style={{ padding: "10px 0" }} key={m.user_id}>
            <div>
              <div style={{ fontWeight: 600 }}>
                {m.first_name} {m.last_name}
                {m.user_id === me.data?.user.id && <span className="muted"> (you)</span>}
              </div>
              <span className={`pill ${m.role === "admin" ? "accent" : ""}`}>{m.role}</span>
            </div>
            {isAdmin && m.user_id !== me.data?.user.id && (
              <button
                className="btn-small btn-danger"
                style={{ width: "auto" }}
                onClick={() => {
                  if (confirm(`Remove ${m.first_name} from the family?`))
                    removeMember.mutate(m.user_id);
                }}
              >
                Remove
              </button>
            )}
          </div>
        ))}
      </div>

      {isAdmin && (
        <>
          <h2>Invite someone</h2>
          <div className="card">
            {invite ? (
              <>
                <div className="invite-code">{invite.code}</div>
                <p className="muted small" style={{ textAlign: "center", marginBottom: 10 }}>
                  Single use · expires {new Date(invite.expires_at).toLocaleString()}
                </p>
                <button onClick={shareInvite}>Share code</button>
              </>
            ) : (
              <button onClick={() => createInvite.mutate()} disabled={createInvite.isPending}>
                {createInvite.isPending ? "Generating…" : "Generate invite code"}
              </button>
            )}
          </div>
        </>
      )}
    </>
  );
}
