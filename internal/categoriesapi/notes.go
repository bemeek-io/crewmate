package categoriesapi

import (
	"net/http"
	"strings"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
)

// Unmatched handles GET /api/notes/unmatched — notes found on transactions
// that name no category and haven't been ignored. Each is either a label worth
// promoting into the category list or a one-off annotation to ignore.
func (h *Handlers) Unmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	notes, err := h.Store.UnmatchedNotes(ctx, family.FamilyID(ctx), 50)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load notes")
		return
	}
	ignored, err := h.Store.ListIgnoredNotes(ctx, family.FamilyID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load ignored notes")
		return
	}
	items := make([]map[string]any, 0, len(notes))
	for _, n := range notes {
		items = append(items, map[string]any{
			"note": n.Note, "count": n.Count, "last_seen": n.LastSeen,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"notes": items, "ignored": ignored})
}

// Ignore handles POST /api/notes/ignore — stop offering to turn this note into
// a category. The note itself is left untouched in Crew.
func (h *Handlers) Ignore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "note is required")
		return
	}
	if err := h.Store.IgnoreNote(r.Context(), family.FamilyID(r.Context()), req.Note); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not ignore note")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Unignore handles DELETE /api/notes/ignore.
func (h *Handlers) Unignore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if err := h.Store.UnignoreNote(r.Context(), family.FamilyID(r.Context()), req.Note); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not restore note")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
