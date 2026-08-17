// Package categoriesapi serves category and merchant-rule CRUD.
package categoriesapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
}

func catJSON(c store.Category) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "emoji": c.Emoji, "color": c.Color}
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

func validCategoryInput(w http.ResponseWriter, name, emoji, color string) bool {
	if name == "" || len(name) > 40 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "name must be 1-40 characters")
		return false
	}
	if len(emoji) > 16 || len(color) > 16 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "emoji/color too long")
		return false
	}
	return true
}

// Create handles POST /api/categories.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Emoji string `json:"emoji"`
		Color string `json:"color"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validCategoryInput(w, req.Name, req.Emoji, req.Color) {
		return
	}
	ctx := r.Context()
	c, err := h.Store.CreateCategory(ctx, family.FamilyID(ctx), auth.UserID(ctx), req.Name, req.Emoji, req.Color)
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
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Emoji string `json:"emoji"`
		Color string `json:"color"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validCategoryInput(w, req.Name, req.Emoji, req.Color) {
		return
	}
	err = h.Store.UpdateCategory(r.Context(), family.FamilyID(r.Context()), id, req.Name, req.Emoji, req.Color)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "category not found")
		return
	}
	if err != nil {
		if store.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "duplicate", "a category with that name already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update category")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/categories/{id}. Merchant rules cascade;
// transactions keep their history with category set NULL.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	err = h.Store.DeleteCategory(r.Context(), family.FamilyID(r.Context()), id)
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

// ListRules handles GET /api/merchant-rules — the review surface for the LLM cache.
func (h *Handlers) ListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Store.ListMerchantRules(r.Context(), family.FamilyID(r.Context()))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load rules")
		return
	}
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"id":            rule.ID,
			"merchant_key":  rule.MerchantKey,
			"category_id":   rule.CategoryID,
			"category_name": rule.CategoryName,
			"source":        rule.Source,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rules": out})
}

// UpdateRule handles PATCH /api/merchant-rules/{id}: {category_id}.
func (h *Handlers) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		CategoryID uuid.UUID `json:"category_id"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	err = h.Store.UpdateMerchantRule(r.Context(), family.FamilyID(r.Context()), id, req.CategoryID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "rule not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update rule")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteRule handles DELETE /api/merchant-rules/{id}.
func (h *Handlers) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	err = h.Store.DeleteMerchantRule(r.Context(), family.FamilyID(r.Context()), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "rule not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete rule")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
