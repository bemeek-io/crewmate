import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { post, patch, del } from "./client";
import type { Category } from "./types";

type CategoryList = { categories: Category[] };

const KEY = ["categories"];

/**
 * Optimistic category mutations: the list updates the instant you act, then
 * reconciles with the server. Every hook cancels in-flight refetches, snapshots
 * the previous list for rollback, and invalidates on settle.
 */
function useOptimistic<TArgs>(
  mutationFn: (args: TArgs) => Promise<unknown>,
  apply: (current: Category[], args: TArgs) => Category[],
  alsoInvalidate: string[][] = []
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn,
    onMutate: async (args: TArgs) => {
      await qc.cancelQueries({ queryKey: KEY });
      const previous = qc.getQueryData<CategoryList>(KEY);
      if (previous) {
        qc.setQueryData<CategoryList>(KEY, { categories: apply(previous.categories, args) });
      }
      return { previous };
    },
    onError: (_e, _args, ctx) => {
      const previous = (ctx as { previous?: CategoryList } | undefined)?.previous;
      if (previous) qc.setQueryData(KEY, previous);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: KEY });
      for (const k of alsoInvalidate) qc.invalidateQueries({ queryKey: k });
    },
  });
}

const byName = (a: Category, b: Category) =>
  a.name.toLowerCase().localeCompare(b.name.toLowerCase());

export function useCreateCategory() {
  return useOptimistic<{ name: string; color?: string }>(
    (v) => post<Category>("/api/categories", { name: v.name, color: v.color ?? "" }),
    (current, v) =>
      [
        ...current,
        // Temporary id; replaced when the server response lands.
        {
          id: `pending-${v.name}`,
          name: v.name,
          color: v.color ?? "",
          system: false,
          system_key: null,
          exclude_from_llm: false,
          usage_count: 0,
        } satisfies Category,
      ].sort(byName),
    [["notes", "unmatched"], ["transactions"]]
  );
}

/** Edit a category's name and/or color. */
export function useUpdateCategory() {
  return useOptimistic<{
    category: Category;
    name?: string;
    color?: string;
    exclude_from_llm?: boolean;
  }>(
    (v) =>
      patch(`/api/categories/${v.category.id}`, {
        name: v.name ?? v.category.name,
        color: v.color ?? v.category.color,
        exclude_from_llm: v.exclude_from_llm ?? v.category.exclude_from_llm,
      }),
    (current, v) =>
      current
        .map((c) =>
          c.id === v.category.id
            ? {
                ...c,
                name: v.name ?? c.name,
                color: v.color ?? c.color,
                exclude_from_llm: v.exclude_from_llm ?? c.exclude_from_llm,
              }
            : c
        )
        .sort(byName),
    [["transactions"]]
  );
}

export function useDeleteCategory() {
  return useOptimistic<string>(
    (id) => del(`/api/categories/${id}`),
    (current, id) => current.filter((c) => c.id !== id),
    [["notes", "unmatched"], ["transactions"]]
  );
}

/** Promote a note found on transactions into a reusable category. */
export function useAddCategoryFromNote() {
  return useCreateCategory();
}

export function useIgnoreNote() {
  const qc: QueryClient = useQueryClient();
  return useMutation({
    mutationFn: (note: string) => post("/api/notes/ignore", { note }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["notes", "unmatched"] });
      qc.invalidateQueries({ queryKey: ["transactions"] });
      qc.invalidateQueries({ queryKey: ["transaction"] });
    },
  });
}

export function useUnignoreNote() {
  const qc: QueryClient = useQueryClient();
  return useMutation({
    mutationFn: (note: string) => del("/api/notes/ignore", { note }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["notes", "unmatched"] });
      qc.invalidateQueries({ queryKey: ["transactions"] });
    },
  });
}
