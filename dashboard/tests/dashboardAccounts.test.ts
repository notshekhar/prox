import { describe, expect, test } from 'bun:test';
import { buildAccountRows, countReady } from '../src/features/dashboard/accounts';
import type { AuthFileItem } from '../src/types/authFile';

const file = (overrides: Partial<AuthFileItem>): AuthFileItem => ({
  name: 'claude-a.json',
  type: 'claude',
  email: 'a@example.com',
  ...overrides,
});

describe('account board rows', () => {
  test('puts accounts that need attention first and switched-off ones last', () => {
    const rows = buildAccountRows([
      file({ name: 'off', disabled: true }),
      file({ name: 'ok' }),
      file({ name: 'bad', unavailable: true, statusMessage: 'token expired' }),
    ]);
    expect(rows.map((row) => row.id)).toEqual(['bad', 'ok', 'off']);
    expect(rows[0].note).toBe('token expired');
    expect(rows[1].note).toBe('');
  });

  test('a disabled account stays off even if it is also unavailable', () => {
    const [row] = buildAccountRows([file({ disabled: true, unavailable: true })]);
    expect(row.state).toBe('off');
  });

  test('never labels a row with the account field, which can hold an API key', () => {
    const [row] = buildAccountRows([
      file({ email: undefined, name: 'key.json', account: 'sk-secret' } as Partial<AuthFileItem>),
    ]);
    expect(row.label).toBe('key.json');
  });

  test('reports no success rate until an account has served a request', () => {
    const [idle] = buildAccountRows([file({})]);
    expect(idle.total).toBe(0);
    expect(idle.successRate).toBeNull();

    const [busy] = buildAccountRows([
      file({ success: 3, failed: 1 } as Partial<AuthFileItem>),
    ]);
    expect(busy.total).toBe(4);
    expect(busy.successRate).toBe(75);
  });

  test('counts only accounts that can serve', () => {
    const rows = buildAccountRows([
      file({ name: 'a' }),
      file({ name: 'b', disabled: true }),
      file({ name: 'c', unavailable: true }),
    ]);
    expect(countReady(rows)).toBe(1);
  });
});
