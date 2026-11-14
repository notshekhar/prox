import { apiClient } from './client';

/** Capture before a config read and check immediately before its dependent write. */
export function guardConfigConnection(): () => void {
  const revision = apiClient.getConnectionRevision();
  return () => {
    if (revision !== apiClient.getConnectionRevision()) {
      throw new DOMException('The management connection changed.', 'AbortError');
    }
  };
}

let sharedConfigRead: { revision: number; request: Promise<unknown> } | null = null;

/**
 * Reads the whole config, joining a read already in flight. A page mounts several
 * views that each want it, and they would otherwise repeat the same request. Each
 * caller gets its own copy, and nothing is kept once the read finishes.
 */
export async function readConfigDocument(): Promise<unknown> {
  const revision = apiClient.getConnectionRevision();
  if (sharedConfigRead?.revision !== revision) {
    const read = {
      revision,
      request: apiClient.get<unknown>('/config').finally(() => {
        if (sharedConfigRead === read) sharedConfigRead = null;
      }),
    };
    sharedConfigRead = read;
  }
  return structuredClone(await sharedConfigRead.request);
}

/** Only an absent persisted config field is optional, not a missing API route. */
export const isMissingConfigValue = (error: unknown): boolean =>
  typeof error === 'object' &&
  error !== null &&
  'status' in error &&
  error.status === 404 &&
  'apiCode' in error &&
  error.apiCode === 'not_found';

export async function getConfigValue<T>(path: string, defaultValue: T): Promise<T> {
  try {
    return await apiClient.get<T>(path);
  } catch (error) {
    if (isMissingConfigValue(error)) return defaultValue;
    throw error;
  }
}
