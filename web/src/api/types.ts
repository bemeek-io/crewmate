export interface Me {
  user: { id: string; first_name: string; last_name: string };
  crew_status: "none" | "active" | "needs_relogin" | "disabled";
  family_id?: string;
  role?: "admin" | "member";
}

export interface Subaccount {
  id: string;
  name: string;
  subaccountType: string;
  overallBalance: number; // cents
  goal: number | null;
}

export interface Account {
  id: string;
  type: string;
  name: string;
  overallBalance: number; // cents
  subaccounts: Subaccount[] | null;
}

export interface MemberAccounts {
  user_id: string;
  first_name: string;
  fetched_at: string;
  accounts: Account[] | null;
}

export interface Txn {
  id: string;
  amount_cents: number;
  payee: string;
  title: string;
  description: string;
  status: string;
  type: string;
  mcc: string;
  image_url: string;
  subaccount_name: string;
  occurred_at: string;
  cleared_at: string | null;
  pending: boolean;
  /** Crew's note field — the source of truth for the category. */
  note: string;
  /** Derived by matching `note` against the family's category list. */
  category_id: string | null;
  category_name: string | null;
  /** True when the note is a hand-written annotation, not a category. */
  has_user_note: boolean;
  /** The family chose not to treat this note as a category. */
  note_ignored: boolean;
  /** The note could be promoted into the category list. */
  can_add_category: boolean;
  recurring_id: string | null;
  merchant_key?: string;
}

export interface Category {
  id: string;
  name: string;
  color: string;
}

export interface RecurringSeries {
  id: string;
  merchant_key: string;
  /** subscription = fixed amount on a steady schedule; recurring = repeats but varies. */
  kind: "subscription" | "recurring" | "none";
  is_subscription: boolean;
  typical_amount_cents: number;
  min_amount_cents: number;
  max_amount_cents: number;
  cadence: string;
  period_days: number | null;
  /** Evidence behind the classification. */
  interval_spread_pct: number;
  amount_spread_pct: number;
  day_spread_days: number;
  first_seen_at: string;
  last_seen_at: string;
  occurrence_count: number;
  dismissed: boolean;
}

export interface UnmatchedNote {
  note: string;
  count: number;
  last_seen: string;
}

export interface FamilyInfo {
  id: string;
  name: string;
  role: "admin" | "member";
  members: {
    user_id: string;
    first_name: string;
    last_name: string;
    role: string;
    joined_at: string;
  }[];
}
