// Package categoriesapi serves CRUD for a family's reusable category list.
// This list is the only categorization state crewmate stores; a transaction's
// category lives in Crew's note field.
package categoriesapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

// renameLimit caps how many transaction notes one rename rewrites in Crew.
const renameLimit = 500

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
	// Pipeline backfills categories over past transactions. Optional: nil
	// simply means those endpoints report nothing to do.
	Pipeline *categorize.Pipeline
}

func catJSON(c store.Category) map[string]any {
	return map[string]any{
		"id":    c.ID,
		"name":  c.Name,
		"color": c.Color,
		// System categories can be recolored but not renamed or removed.
		"system_key": c.SystemKey,
		"system":     c.SystemKey != nil,
		// Withheld from auto-categorization; still usable by hand and by rules.
		"exclude_from_llm": c.ExcludeFromLLM,
		// How many transactions carry it, so a picker leads with what's used.
		"usage_count": c.UsageCount,
	}
}

// List handles GET /api/categories.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	cats, err := h.Store.ListCategories(r.Context(), family.FamilyID(r.Context()))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load categories")
		return
	}
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		out = append(out, catJSON(c))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"categories": out})
}

func validCategoryInput(w http.ResponseWriter, name, color string) bool {
	if name == "" || len(name) > 40 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "name must be 1-40 characters")
		return false
	}
	if len(color) > 16 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "color too long")
		return false
	}
	return true
}

// Create handles POST /api/categories.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validCategoryInput(w, req.Name, req.Color) {
		return
	}
	ctx := r.Context()
	c, err := h.Store.CreateCategory(ctx, family.FamilyID(ctx), auth.UserID(ctx), req.Name, req.Color)
	if err != nil {
		if store.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "duplicate", "a category with that name already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create category")
		return
	}
	httpx.JSON(w, http.StatusCreated, catJSON(*c))
}

// Update handles PATCH /api/categories/{id}.
//
// Renaming matters more than it looks: transaction categories are the note
// text in Crew, so a rename must rewrite every note that carries the old name,
// or those transactions silently become uncategorized.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		Name           string `json:"name"`
		Color          string `json:"color"`
		ExcludeFromLLM bool   `json:"exclude_from_llm"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validCategoryInput(w, req.Name, req.Color) {
		return
	}

	existing, err := h.Store.GetCategory(ctx, famID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "category not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load category")
		return
	}

	if existing.SystemKey != nil && !strings.EqualFold(existing.Name, req.Name) {
		httpx.Error(w, http.StatusBadRequest, "system_category",
			"this category is built in and cannot be renamed — you can still change its color")
		return
	}

	if err := h.Store.UpdateCategory(ctx, famID, id, req.Name, req.Color, req.ExcludeFromLLM); err != nil {
		if store.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "duplicate", "a category with that name already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update category")
		return
	}

	requeued := 0
	if !strings.EqualFold(existing.Name, req.Name) {
		txns, err := h.Store.TransactionsWithNote(ctx, famID, existing.Name, renameLimit)
		if err != nil {
			h.Log.Warn("rename note lookup", zap.Error(err))
		}
		for _, t := range txns {
			if err := h.Store.EnqueueNoteWrite(ctx, t.ConnectionID, t.CrewTxnID, req.Name); err != nil {
				h.Log.Warn("queue rename note", zap.Error(err))
				continue
			}
			requeued++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "notes_requeued": requeued})
}

// Delete handles DELETE /api/categories/{id}. Notes in Crew are left intact —
// those transactions simply read as uncategorized until relabeled.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	ctx := r.Context()
	existing, err := h.Store.GetCategory(ctx, family.FamilyID(ctx), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "category not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load category")
		return
	}
	if existing.SystemKey != nil {
		httpx.Error(w, http.StatusBadRequest, "system_category",
			"this category is built in and cannot be deleted")
		return
	}
	err = h.Store.DeleteCategory(ctx, family.FamilyID(ctx), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "category not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete category")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
