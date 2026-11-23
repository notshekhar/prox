import { apiClient } from './client';
import { isRecord } from '@/utils/helpers';

export interface LocalModelInstall {
  active: boolean;
  step: string;
  error: string;
}

export interface LocalModel {
  id: string;
  provider: string;
  providerName: string;
  description: string;
  kind: string;
  repo: string;
  downloadSize: string;
  installed: boolean;
  running: boolean;
  device: string;
  install: LocalModelInstall | null;
}

const asString = (value: unknown): string => (typeof value === 'string' ? value : '');

const normalizeInstall = (value: unknown): LocalModelInstall | null => {
  if (!isRecord(value)) return null;
  return {
    active: value.active === true,
    step: asString(value.step),
    error: asString(value.error),
  };
};

const normalizeLocalModel = (value: unknown): LocalModel | null => {
  if (!isRecord(value) || !asString(value.id)) return null;
  return {
    id: asString(value.id),
    provider: asString(value.provider),
    providerName: asString(value.provider_name),
    description: asString(value.description),
    kind: asString(value.kind),
    repo: asString(value.repo),
    downloadSize: asString(value.download_size),
    installed: value.installed === true,
    running: value.running === true,
    device: asString(value.device),
    install: normalizeInstall(value.install),
  };
};

export const localModelsApi = {
  async list(): Promise<LocalModel[]> {
    const data = await apiClient.get('/local-models');
    const models = isRecord(data) && Array.isArray(data.models) ? data.models : [];
    return models.map(normalizeLocalModel).filter((m): m is LocalModel => m !== null);
  },

  async install(id: string): Promise<void> {
    await apiClient.post(`/local-models/${encodeURIComponent(id)}/install`);
  },

  async remove(id: string): Promise<void> {
    await apiClient.delete(`/local-models/${encodeURIComponent(id)}`);
  },
};
