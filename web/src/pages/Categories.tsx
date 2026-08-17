import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import {
  useCreateCategory,
  useUpdateCategory,
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

function ColorPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (c: string) => void;
}) {
  return (
    <div className="color-picker">
      {COLORS.map((c) => (
        <button
          type="button"
          key={c}
          aria-label={`Use color ${c}`}
          aria-pressed={value === c}
          style={{ background: c }}
          onClick={() => onChange(c)}
        />
      ))}
    </div>
  );
}

export default function Categories() {
  const [name, setName] = useState("");
  const [color, setColor] = useState(COLORS[0]);
  const [editing, setEditing] = useState<Category | null>(null);
  const [editName, setEditName] = useState("");
  const [editColor, setEditColor] = useState("");

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => get<{ categories: Category[] }>("/api/categories"),
  });
  const unmatched = useQuery({
    queryKey: ["notes", "unmatched"],
    queryFn: () => get<{ notes: UnmatchedNote[]; ignored: string[] }>("/api/notes/unmatched"),
  });

  const create = useCreateCategory();
  const update = useUpdateCategory();
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

  function startEdit(c: Category) {
    setEditing(c);
    setEditName(c.name);
    setEditColor(c.color || COLORS[0]);
  }

  function saveEdit(c: Category) {
    update.mutate({ category: c, name: editName.trim() || c.name, color: editColor });
    setEditing(null);
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
          <ColorPicker value={color} onChange={setColor} />
          {create.isError && <div className="error">Could not add that category.</div>}
        </form>
      </div>

      {notes.length > 0 && (
        <>
          <h2>Notes found in Crew</h2>
          <p className="muted small" style={{ marginTop: -8, marginBottom: 12 }}>
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

      <div className="section-header">
        <h2>Your categories</h2>
        <Link to="/rules">Rules</Link>
      </div>
      <div className="card">
        {(categories.data?.categories ?? []).map((c) =>
          editing?.id === c.id ? (
            <div style={{ padding: "12px 0" }} key={c.id}>
              {c.system ? (
                <p className="muted small" style={{ marginBottom: 10 }}>
                  <b>{c.name}</b> is built in — it can be recolored, but not renamed or deleted.
                </p>
              ) : (
                <input
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  maxLength={40}
                  autoFocus
                />
              )}
              <ColorPicker value={editColor} onChange={setEditColor} />
              <div className="row" style={{ gap: 8, marginTop: 12 }}>
                <button
                  className="btn-small"
                  style={{ width: "auto" }}
                  disabled={!editName.trim()}
                  onClick={() => saveEdit(c)}
                >
                  Save
                </button>
                <button
                  className="btn-small btn-secondary"
                  style={{ width: "auto" }}
                  onClick={() => setEditing(null)}
                >
                  Cancel
                </button>
                <span className="grow" />
                {!c.system && (
                <button
                  className="btn-small btn-danger"
                  style={{ width: "auto" }}
                  onClick={() => {
                    if (
                      confirm(
                        `Delete "${c.name}"? Transactions keep their note in Crew but will show as Misc.`
                      )
                    ) {
                      remove.mutate(c.id);
                      setEditing(null);
                    }
                  }}
                >
                  Delete
                </button>
                )}
              </div>
            </div>
          ) : (
            // The whole row is the edit affordance, so tapping the swatch (or
            // the name) opens name + color editing.
            <button
              className="row spread category-row"
              key={c.id}
              disabled={isPending(c)}
              onClick={() => startEdit(c)}
            >
              <span className="row" style={{ gap: 10, opacity: isPending(c) ? 0.5 : 1 }}>
                <span className="swatch" style={c.color ? { background: c.color } : undefined} />
                <span style={{ fontWeight: 600 }}>{c.name}</span>
                {c.system && <span className="pill">built in</span>}
              </span>
              <span className="muted small">Edit</span>
            </button>
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
