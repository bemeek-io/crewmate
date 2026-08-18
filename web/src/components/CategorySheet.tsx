import { useEffect, useMemo, useRef, useState } from "react";
import type { Category } from "../api/types";
import { CheckIcon, CloseIcon } from "./Icons";

/**
 * A searchable category picker in a bottom sheet.
 *
 * Rendering every category inline stops scaling somewhere around a dozen: the
 * filter row on Activity wrapped into a wall of chips, and the grid on a
 * transaction pushed the Save button off screen. A sheet keeps the page short
 * however many categories a family accumulates, and typing beats hunting once
 * the list is longer than a screen.
 *
 * Multi-select stays open so several can be chosen at once; single-select
 * closes on choice, because the choice was the whole errand.
 */
export default function CategorySheet({
  categories,
  selected,
  multi = false,
  includeUncategorized = false,
  uncategorized = false,
  onToggle,
  onToggleUncategorized,
  onClose,
}: {
  categories: Category[];
  selected: string[];
  multi?: boolean;
  includeUncategorized?: boolean;
  uncategorized?: boolean;
  onToggle: (c: Category) => void;
  onToggleUncategorized?: () => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus on open, but not on a phone: the keyboard would cover the list
  // before anything has been read.
  useEffect(() => {
    if (window.matchMedia("(min-width: 700px)").matches) inputRef.current?.focus();
  }, []);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return categories;
    return categories.filter((c) => c.name.toLowerCase().includes(q));
  }, [categories, query]);

  const isSelected = (c: Category) => selected.includes(c.id);

  return (
    <>
      <div className="sheet-scrim" onClick={onClose} />
      <div className="sheet sheet-tall" role="dialog" aria-label="Choose a category">
        <div className="sheet-head">
          <input
            ref={inputRef}
            type="search"
            placeholder="Search categories"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ marginBottom: 0 }}
          />
          <button className="icon-btn" aria-label="Close" onClick={onClose}>
            <CloseIcon size={16} />
          </button>
        </div>

        <div className="sheet-list">
          {includeUncategorized && !query.trim() && (
            <button
              className={`sheet-item ${uncategorized ? "active" : ""}`}
              onClick={() => {
                onToggleUncategorized?.();
                if (!multi) onClose();
              }}
            >
              <span className="cat-dot" style={{ background: "var(--border)" }} />
              <span className="grow">Uncategorized</span>
              {uncategorized && <CheckIcon size={16} />}
            </button>
          )}

          {matches.map((c) => (
            <button
              key={c.id}
              className={`sheet-item ${isSelected(c) ? "active" : ""}`}
              disabled={c.id.startsWith("pending-")}
              onClick={() => {
                onToggle(c);
                if (!multi) onClose();
              }}
            >
              <span
                className="cat-dot"
                style={{ background: c.color || "var(--border)" }}
                aria-hidden="true"
              />
              <span className="grow">{c.name}</span>
              {isSelected(c) && <CheckIcon size={16} />}
            </button>
          ))}

          {matches.length === 0 && (
            <p className="muted small" style={{ padding: "14px 18px" }}>
              No category matches “{query.trim()}”.
            </p>
          )}
        </div>
      </div>
    </>
  );
}
