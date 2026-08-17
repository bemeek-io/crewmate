import { FormEvent, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import {
  useCreateCategory,
  useRenameCategory,
  useDeleteCategory,
  useIgnoreNote,
  useUnignoreNote,
} from "../api/categories";
import type { Category, UnmatchedNote } from "../api/types";

// A small, deliberately muted palette — categories are identified by a color
// swatch rather than an emoji.
const COLORS = [
  "#4ade80",
  "#38bdf8",
  "#a78bfa",
  "#f472b6",
  "#fb923c",
  "#facc15",
  "#2dd4bf",
  "#f87171",
];

/** True while a category exists only in the optimistic cache. */
const isPending = (c: Category) => c.id.startsWith("pending-");

export default function Categories() {
  const [name, setName] = useState("");
  const [color, setColor] = useState(COLORS[0]);
  const [editing, setEditing] = useState<Category | null>(null);
  const [editName, setEditName] = useState("");

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });
  const unmatched = useQuery({
    queryKey: ["notes", "unmatched"],
    queryFn: () => get<{ notes: UnmatchedNote[]; ignored: string[] }>("/api/notes/unmatched"),
  });

  const create = useCreateCategory();
  const rename = useRenameCategory();
  const remove = useDeleteCategory();
  const ignore = useIgnoreNote();
  const unignore = useUnignoreNote();

  function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    // Optimistic: the row appears before the request resolves.
    create.mutate({ name: trimmed, color });
    setName("");
  }

  const notes = unmatched.data?.notes ?? [];
  const ignored = unmatched.data?.ignored ?? [];

  return (
    <>
      <h1>Categories</h1>
      <p className="muted small" style={{ marginBottom: 12 }}>
        Categories are saved here and shared with your family. Each transaction’s category is
        stored in its note in Crew, so it shows up in the Crew app too — and anything you type
        there syncs back here.
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
            <button className="btn-small" style={{ width: "auto" }}>
              Add
            </button>
          </div>
          <div className="color-picker">
            {COLORS.map((c) => (
              <button
                type="button"
                key={c}
                aria-label={`Use color ${c}`}
                aria-pressed={color === c}
                style={{ background: c }}
                onClick={() => setColor(c)}
              />
            ))}
          </div>
          {create.isError && <div className="error">Could not add that category.</div>}
        </form>
      </div>

      {notes.length > 0 && (
        <>
          <h2>Notes found in Crew</h2>
          <p className="muted small" style={{ marginBottom: 10 }}>
            These notes don’t match a category yet. Add one to start using it, or ignore it if
            it’s just a personal note.
          </p>
          <div className="card">
            {notes.map((n) => (
              <div className="row spread" style={{ padding: "10px 0" }} key={n.note}>
                <div className="grow">
                  <div className="txn-title">{n.note}</div>
                  <div className="muted small">
                    on {n.count} transaction{n.count === 1 ? "" : "s"}
                  </div>
                </div>
                <div className="row" style={{ gap: 6 }}>
                  <button
                    className="btn-small"
                    style={{ width: "auto" }}
                    onClick={() => create.mutate({ name: n.note })}
                  >
                    Add category
                  </button>
                  <button
                    className="btn-small btn-secondary"
                    style={{ width: "auto" }}
                    onClick={() => ignore.mutate(n.note)}
                  >
                    Ignore
                  </button>
                </div>
              </div>
            ))}
          </div>
        </>
      )}

      <h2>Your categories</h2>
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
                disabled={!editName.trim()}
                onClick={() => {
                  rename.mutate({ category: c, name: editName.trim() });
                  setEditing(null);
                }}
              >
                Save
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
              <span
                className="row"
                style={{ fontWeight: 600, gap: 10, opacity: isPending(c) ? 0.5 : 1 }}
              >
                <span className="swatch" style={c.color ? { background: c.color } : undefined} />
                {c.name}
              </span>
              <div className="row" style={{ gap: 6 }}>
                <button
                  className="btn-small btn-secondary"
                  style={{ width: "auto" }}
                  // Optimistic rows have a placeholder id until the server
                  // responds; editing one would target a nonexistent record.
                  disabled={isPending(c)}
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
                  disabled={isPending(c)}
                  onClick={() => {
                    if (
                      confirm(
                        `Delete "${c.name}"? Transactions keep their note in Crew but will show as Misc.`
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
      <p className="muted small">
        Renaming updates the note on past transactions in Crew too, so nothing loses its category.
      </p>

      {ignored.length > 0 && (
        <>
          <h2>Ignored notes</h2>
          <div className="card">
            {ignored.map((n) => (
              <div className="row spread" style={{ padding: "8px 0" }} key={n}>
                <span className="muted">{n}</span>
                <button
                  className="btn-small btn-secondary"
                  style={{ width: "auto" }}
                  onClick={() => unignore.mutate(n)}
                >
                  Un-ignore
                </button>
              </div>
            ))}
          </div>
        </>
      )}
    </>
  );
}
