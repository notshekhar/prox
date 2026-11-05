import type { AuthFileItem } from '@/types/authFile';
import { normalizeRecentRequestUsageEntry } from '@/utils/recentRequests';

export type AccountState = 'ready' | 'off' | 'problem';

export interface AccountRow {
  id: string;
  label: string;
  provider: string;
  state: AccountState;
  /** Why the account isn't serving. Only set for accounts with a problem. */
  note: string;
  total: number;
  /** 0–100, or null when the account has served nothing. */
  successRate: number | null;
  activity: number[];
}

const providerOf = (file: AuthFileItem): string => {
  const provider = String(file.type ?? file.provider ?? '')
    .trim()
    .toLowerCase();
  return provider && provider !== 'empty' ? provider : 'unknown';
};

const stateOf = (file: AuthFileItem): AccountState => {
  if (file.disabled) return 'off';
  return file.unavailable ? 'problem' : 'ready';
};

const STATE_ORDER: Record<AccountState, number> = { problem: 0, ready: 1, off: 2 };

// file.account is deliberately not used: for API-key credentials it can hold
// the key itself.
const toRow = (file: AuthFileItem): AccountRow => {
  const usage = normalizeRecentRequestUsageEntry(file);
  const total = usage.success + usage.failed;
  const state = stateOf(file);
  return {
    id: file.name,
    label: file.email || file.projectId || file.name,
    provider: providerOf(file),
    state,
    note: state === 'problem' ? (file.statusMessage ?? '') : '',
    total,
    successRate: total > 0 ? (usage.success / total) * 100 : null,
    activity: usage.recentRequests.map((bucket) => bucket.success + bucket.failed),
  };
};

/** Accounts that need action come first, then the rest by provider and name. */
export const buildAccountRows = (files: AuthFileItem[]): AccountRow[] =>
  files
    .map(toRow)
    .sort(
      (a, b) =>
        STATE_ORDER[a.state] - STATE_ORDER[b.state] ||
        a.provider.localeCompare(b.provider) ||
        a.label.localeCompare(b.label)
    );

export const countReady = (rows: AccountRow[]): number =>
  rows.filter((row) => row.state === 'ready').length;
