import { describe, expect, test } from 'bun:test';
import type { LocalModel } from '../src/services/api/localModels';
import {
  isAnyInstalling,
  localModelStatus,
  systemOneExample,
} from '../src/features/localModels/localModelStatus';

const model = (patch: Partial<LocalModel> = {}): LocalModel => ({
  id: 'julia-2-flash',
  provider: 'supersonic',
  providerName: 'Supersonic Labs',
  description: '',
  kind: 'decision',
  repo: 'SupersonicLabs/Julia-2-Flash',
  downloadSize: '430 MB',
  installed: false,
  running: false,
  device: '',
  install: null,
  ...patch,
});

describe('localModelStatus', () => {
  test('a model on disk offers removal; a missing one offers install', () => {
    expect(localModelStatus(model())).toMatchObject({ tone: 'idle', action: 'install' });
    expect(localModelStatus(model({ installed: true }))).toMatchObject({
      tone: 'ready',
      action: 'remove',
    });
  });

  test('a running model shows the device it runs on', () => {
    const status = localModelStatus(model({ installed: true, running: true, device: 'mps' }));
    expect(status).toMatchObject({ tone: 'live', detail: 'mps', action: 'remove' });
  });

  test('an install in progress shows its step and hides the buttons', () => {
    const install = { active: true, step: 'Downloading the model', error: '' };
    expect(localModelStatus(model({ install }))).toMatchObject({
      tone: 'busy',
      detail: 'Downloading the model',
      action: 'none',
    });
    expect(isAnyInstalling([model(), model({ install })])).toBe(true);
    expect(isAnyInstalling([model()])).toBe(false);
  });

  test('a failed install shows why and offers a retry', () => {
    const install = { active: false, step: '', error: 'uv is not installed' };
    expect(localModelStatus(model({ install }))).toMatchObject({
      tone: 'error',
      detail: 'uv is not installed',
      action: 'install',
    });
  });

  test('the example request targets this prox and the installed model', () => {
    const curl = systemOneExample('http://127.0.0.1:8317', 'julia-2-flash');
    expect(curl).toContain('curl http://127.0.0.1:8317/v1/systemone');
    expect(curl).toContain('"model": "julia-2-flash"');
  });
});
