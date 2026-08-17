import { FormEvent, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, patch, del, ApiError } from "../api/client";
import type { Category } from "../api/types";

const EMOJI_SUGGESTIONS = ["🛒", "🍔", "⛽", "🏠", "🎬", "👕", "💊", "🎁", "✈️", "📱", "🐾", "🎓"];

export default function Categories() {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [emoji, setEmoji] = useState("");
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<Category | null>(null);
  const [editName, setEditName] = useState("");

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });

  const invalidate = async () => {
    await qc.invalidateQueries({ queryKey: ["categories"] });
    await qc.invalidateQueries({ queryKey: ["transactions"] });
  };

  const create = useMutation({
    mutationFn: () => post("/api/categories", { name: name.trim(), emoji, color: "" }),
    onSuccess: async () => {
      setName("");
      setEmoji("");
      setError("");
      await invalidate();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not create"),
  });

  const rename = useMutation({
    mutationFn: (c: Category) =>
      patch<{ notes_requeued: number }>(`/api/categories/${c.id}`, {
        name: editName.trim(),
        emoji: c.emoji,
        color: c.color,
      }),
    onSuccess: async () => {
      setEditing(null);
      await invalidate();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not rename"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => del(`/api/categories/${id}`),
    onSuccess: invalidate,
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (name.trim()) create.mutate();
  }

  return (
    <>
      <h1>Categories</h1>
      <p className="muted small" style={{ marginBottom: 12 }}>
        Categories are saved here and shared with your family. Each transaction’s category is
        stored in its note in Crew, so it shows up in the Crew app too.
      </p>

      <div className="card">
        <form onSubmit={submit}>
          <div className="row">
            <input
              placeholder="New category (e.g. Groceries)"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={40}
              style={{ marginBottom: 0 }}
            />
            <button className="btn-small" style={{ width: "auto" }} disabled={create.isPending}>
              Add
            </button>
          </div>
          <div className="row" style={{ marginTop: 8, flexWrap: "wrap", gap: 6 }}>
            {EMOJI_SUGGESTIONS.map((e2) => (
              <button
                type="button"
                key={e2}
                className="btn-small btn-secondary"
                style={{ width: "auto", opacity: emoji === e2 ? 1 : 0.6 }}
                onClick={() => setEmoji(emoji === e2 ? "" : e2)}
              >
                {e2}
              </button>
            ))}
          </div>
          {error && <div className="error">{error}</div>}
        </form>
      </div>

      <div className="card">
        {(categories.data?.categories ?? []).map((c) =>
          editing?.id === c.id ? (
            <div className="row" style={{ padding: "10px 0" }} key={c.id}>
              <input
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                maxLength={40}
                style={{ marginBottom: 0 }}
                autoFocus
              />
              <button
                className="btn-small"
                style={{ width: "auto" }}
                disabled={rename.isPending || !editName.trim()}
                onClick={() => rename.mutate(c)}
              >
                {rename.isPending ? "…" : "Save"}
              </button>
              <button
                className="btn-small btn-secondary"
                style={{ width: "auto" }}
                onClick={() => setEditing(null)}
              >
                ✕
              </button>
            </div>
          ) : (
            <div className="row spread" style={{ padding: "10px 0" }} key={c.id}>
              <span style={{ fontWeight: 600 }}>
                {c.emoji ? `${c.emoji} ` : ""}
                {c.name}
              </span>
              <div className="row" style={{ gap: 6 }}>
                <button
                  className="btn-small btn-secondary"
                  style={{ width: "auto" }}
                  onClick={() => {
                    setEditing(c);
                    setEditName(c.name);
                  }}
                >
                  Rename
                </button>
                <button
                  className="btn-small btn-danger"
                  style={{ width: "auto" }}
                  onClick={() => {
                    if (
                      confirm(
                        `Delete "${c.name}"? Transactions keep their note in Crew but will show as uncategorized.`
                      )
                    )
                      remove.mutate(c.id);
                  }}
                >
                  Delete
                </button>
              </div>
            </div>
          )
        )}
        {categories.data && categories.data.categories.length === 0 && (
          <p className="muted">
            No categories yet. Add a few — new transactions are matched against them automatically.
          </p>
        )}
      </div>

      {editing && (
        <p className="muted small">
          Renaming updates the note on past transactions in Crew too, so nothing loses its
          category.
        </p>
      )}
    </>
  );
}
