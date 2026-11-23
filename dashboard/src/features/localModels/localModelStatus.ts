import type { LocalModel } from '@/services/api/localModels';

export type LocalModelTone = 'idle' | 'busy' | 'ready' | 'live' | 'error';

export interface LocalModelStatus {
  tone: LocalModelTone;
  labelKey: string;
  detail: string;
  action: 'install' | 'remove' | 'none';
}

/** One row's status: an install in progress wins, then a failed install, then what is on disk. */
export function localModelStatus(model: LocalModel): LocalModelStatus {
  const install = model.install;
  if (install?.active) {
    return {
      tone: 'busy',
      labelKey: 'local_models.status.installing',
      detail: install.step,
      action: 'none',
    };
  }
  if (install?.error && !model.installed) {
    return {
      tone: 'error',
      labelKey: 'local_models.status.failed',
      detail: install.error,
      action: 'install',
    };
  }
  if (model.running) {
    return {
      tone: 'live',
      labelKey: 'local_models.status.running',
      detail: model.device,
      action: 'remove',
    };
  }
  if (model.installed) {
    return {
      tone: 'ready',
      labelKey: 'local_models.status.installed',
      detail: '',
      action: 'remove',
    };
  }
  return {
    tone: 'idle',
    labelKey: 'local_models.status.not_installed',
    detail: '',
    action: 'install',
  };
}

/** Poll while anything is installing so the step updates without a refresh. */
export const isAnyInstalling = (models: LocalModel[]): boolean =>
  models.some((model) => model.install?.active === true);

/** A ready-to-paste request for a decision model, against this prox. */
export function systemOneExample(origin: string, modelId: string): string {
  return [
    `curl ${origin}/v1/systemone \\`,
    `  -H "Authorization: Bearer $PROX_KEY" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{`,
    `    "model": "${modelId}",`,
    `    "state": "Help! My payouts have been failing for 3 days.",`,
    `    "questions": {`,
    `      "is_urgent": { "type": "noul", "instructions": "Does this convey urgency?" },`,
    `      "team": { "type": "choice", "criteria": { "billing": "Payments", "technical": "Bugs" } }`,
    `    }`,
    `  }'`,
  ].join('\n');
}
