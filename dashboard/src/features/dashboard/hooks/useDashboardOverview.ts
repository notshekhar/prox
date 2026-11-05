import { useCallback, useEffect, useMemo, useState } from 'react';
import { authFilesApi } from '@/services/api';
import { useAuthStore, useConfigStore, useModelsStore } from '@/stores';
import { useApiKeysForModels } from '@/hooks/useApiKeysForModels';
import { useProviderRecentRequests } from '@/components/providers/hooks/useProviderRecentRequests';
import {
  mergeRecentRequestBucketGroups,
  normalizeRecentRequestUsageEntry,
  type RecentRequestBucket,
} from '@/utils/recentRequests';
import type { Config } from '@/types';
import type { AuthFileItem } from '@/types/authFile';
import { buildAccountRows } from '../accounts';
import {
  TRAFFIC_BUCKET_MINUTES,
  type DashboardCounts,
  type TrafficWindow,
} from '../types';

const EMPTY_TRAFFIC: TrafficWindow = {
  buckets: [],
  totalSuccess: 0,
  totalFailure: 0,
  total: 0,
  successRate: null,
  peakTotal: 0,
  peakIndex: -1,
  windowMinutes: 0,
};

/** `api-key-usage` 的键形如 `<baseUrl>|<apiKey>`，取第一个分隔符之后的部分 */
const apiKeyFromCompositeKey = (compositeKey: string): string => {
  const separatorIndex = compositeKey.indexOf('|');
  return separatorIndex < 0 ? '' : compositeKey.slice(separatorIndex + 1).trim();
};

const buildTrafficWindow = (bucketGroups: RecentRequestBucket[][]): TrafficWindow => {
  const buckets = mergeRecentRequestBucketGroups(bucketGroups);
  if (buckets.length === 0) {
    return EMPTY_TRAFFIC;
  }

  let totalSuccess = 0;
  let totalFailure = 0;
  let peakTotal = 0;
  let peakIndex = -1;

  buckets.forEach((bucket, index) => {
    const bucketTotal = bucket.success + bucket.failed;
    totalSuccess += bucket.success;
    totalFailure += bucket.failed;
    if (bucketTotal > peakTotal) {
      peakTotal = bucketTotal;
      peakIndex = index;
    }
  });

  const total = totalSuccess + totalFailure;

  return {
    buckets,
    totalSuccess,
    totalFailure,
    total,
    successRate: total > 0 ? (totalSuccess / total) * 100 : null,
    peakTotal,
    peakIndex,
    windowMinutes: buckets.length * TRAFFIC_BUCKET_MINUTES,
  };
};

export const getProviderKeyCounts = (config: Config) => ({
  gemini: config.geminiApiKeys?.length ?? 0,
  interactions: config.interactionsApiKeys?.length ?? 0,
  codex: config.codexApiKeys?.length ?? 0,
  meta: config.metaApiKeys?.length ?? 0,
  xai: config.xaiApiKeys?.length ?? 0,
  claude: config.claudeApiKeys?.length ?? 0,
  vertex: config.vertexApiKeys?.length ?? 0,
  openai: config.openaiCompatibility?.length ?? 0,
});

/**
 * 汇总仪表盘所需的全部数据。
 *
 * 流量数据有两个互不重叠的来源：`api-key-usage`（配置内联的 API Key 凭证）
 * 与 `auth-files`（文件/运行时凭证）。后端对二者的判定条件互斥，但插件提供的
 * 凭证理论上可同时命中，因此这里按 `account_type` + `account` 做一次防御性去重。
 */
export function useDashboardOverview() {
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const apiBase = useAuthStore((state) => state.apiBase);
  const config = useConfigStore((state) => state.config);
  const fetchConfig = useConfigStore((state) => state.fetchConfig);

  const models = useModelsStore((state) => state.models);
  const modelsLoading = useModelsStore((state) => state.loading);
  const modelsError = useModelsStore((state) => state.error);
  const fetchModelsFromStore = useModelsStore((state) => state.fetchModels);

  const connected = connectionStatus === 'connected';
  const resolveApiKeysForModels = useApiKeysForModels();

  const { usageByProvider, refreshRecentRequests } = useProviderRecentRequests({
    enabled: connected,
  });

  const [authFiles, setAuthFiles] = useState<AuthFileItem[] | null>(null);

  const loadAuthFiles = useCallback(async () => {
    if (!connected) return;
    try {
      const response = await authFilesApi.list();
      setAuthFiles(response.files);
    } catch {
      setAuthFiles(null);
    }
  }, [connected]);

  const loadModels = useCallback(async () => {
    if (!connected || !apiBase) return;
    try {
      const apiKeys = await resolveApiKeysForModels();
      await fetchModelsFromStore(apiBase, apiKeys[0]);
    } catch {
      // 模型列表失败不应影响仪表盘其余部分
    }
  }, [connected, apiBase, resolveApiKeysForModels, fetchModelsFromStore]);

  useEffect(() => {
    if (!connected) return;
    void fetchConfig().catch(() => undefined);
    void loadAuthFiles();
    void loadModels();
  }, [connected, fetchConfig, loadAuthFiles, loadModels]);

  const refresh = useCallback(async () => {
    if (!connected) return;
    await Promise.allSettled([
      fetchConfig(true),
      loadAuthFiles(),
      loadModels(),
      refreshRecentRequests(),
    ]);
  }, [connected, fetchConfig, loadAuthFiles, loadModels, refreshRecentRequests]);

  const providerKeyCounts = useMemo(() => (config ? getProviderKeyCounts(config) : null), [config]);

  const traffic = useMemo(() => {
    const bucketGroups: RecentRequestBucket[][] = [];
    const apiKeysFromUsage = new Set<string>();

    usageByProvider.forEach((entriesByKey) => {
      entriesByKey.forEach((entry, compositeKey) => {
        const apiKey = apiKeyFromCompositeKey(compositeKey);
        if (apiKey) {
          apiKeysFromUsage.add(apiKey);
        }
        if (entry.recentRequests.length > 0) {
          bucketGroups.push(entry.recentRequests);
        }
      });
    });

    (authFiles ?? []).forEach((file) => {
      const accountType = String(file.account_type ?? '')
        .trim()
        .toLowerCase();
      const account = String(file.account ?? '').trim();
      // A credential already counted by api-key-usage must not be counted twice.
      if (accountType === 'api_key' && account && apiKeysFromUsage.has(account)) {
        return;
      }
      const { recentRequests } = normalizeRecentRequestUsageEntry(file);
      if (recentRequests.length > 0) {
        bucketGroups.push(recentRequests);
      }
    });

    return buildTrafficWindow(bucketGroups);
  }, [usageByProvider, authFiles]);

  const accounts = useMemo(() => buildAccountRows(authFiles ?? []), [authFiles]);

  const counts = useMemo<DashboardCounts>(
    () => ({
      managementKeys: config ? (config.apiKeys?.length ?? 0) : null,
      providerKeys: providerKeyCounts
        ? Object.values(providerKeyCounts).reduce((sum, count) => sum + count, 0)
        : null,
      credentials: authFiles ? authFiles.length : null,
      models: modelsLoading || modelsError ? null : models.length,
    }),
    [config, providerKeyCounts, authFiles, models.length, modelsLoading, modelsError]
  );

  return {
    connectionStatus,
    connected,
    config,
    counts,
    providerKeyCounts,
    traffic,
    accounts,
    accountsLoaded: authFiles !== null,
    refresh,
  };
}
