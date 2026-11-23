import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/Table';
import { localModelsApi, type LocalModel } from '@/services/api/localModels';
import { isAnyInstalling, localModelStatus, systemOneExample } from './localModelStatus';
import styles from './LocalModelsPage.module.scss';

// Fast enough to follow install steps, slow enough not to matter while downloading.
const INSTALL_POLL_MS = 1500;

const errorMessage = (err: unknown): string => (err instanceof Error ? err.message : String(err));

function useLocalModels() {
  const [models, setModels] = useState<LocalModel[] | null>(null);
  const [error, setError] = useState('');

  const refresh = useCallback(async () => {
    try {
      setModels(await localModelsApi.list());
      setError('');
    } catch (err) {
      setError(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const installing = models !== null && isAnyInstalling(models);
  useEffect(() => {
    if (!installing) return undefined;
    const timer = window.setInterval(() => void refresh(), INSTALL_POLL_MS);
    return () => window.clearInterval(timer);
  }, [installing, refresh]);

  return { models, error, setError, refresh };
}

function ModelRow({
  model,
  busy,
  onInstall,
  onRemove,
}: {
  model: LocalModel;
  busy: boolean;
  onInstall: (id: string) => void;
  onRemove: (id: string) => void;
}) {
  const { t } = useTranslation();
  const status = localModelStatus(model);
  return (
    <TableRow>
      <TableCell>
        <div className={styles.modelName}>{model.id}</div>
        <div className={styles.modelDescription}>{model.description}</div>
      </TableCell>
      <TableCell>{model.providerName}</TableCell>
      <TableCell className={styles.mono}>{model.downloadSize}</TableCell>
      <TableCell>
        <span className={styles.status} data-tone={status.tone}>
          <span className={styles.dot} aria-hidden="true" />
          {t(status.labelKey)}
        </span>
        {status.detail ? <div className={styles.statusDetail}>{status.detail}</div> : null}
      </TableCell>
      <TableCell alignRight>
        {status.action === 'install' ? (
          <Button size="sm" loading={busy} onClick={() => onInstall(model.id)}>
            {t('local_models.install')}
          </Button>
        ) : null}
        {status.action === 'remove' ? (
          <Button size="sm" variant="ghost" loading={busy} onClick={() => onRemove(model.id)}>
            {t('local_models.remove')}
          </Button>
        ) : null}
      </TableCell>
    </TableRow>
  );
}

export function LocalModelsPage() {
  const { t } = useTranslation();
  const { models, error, setError, refresh } = useLocalModels();
  const [busyId, setBusyId] = useState('');

  const act = async (id: string, action: (id: string) => Promise<void>) => {
    setBusyId(id);
    try {
      await action(id);
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusyId('');
    }
  };

  const [confirmRemove, setConfirmRemove] = useState('');
  const remove = () => {
    const id = confirmRemove;
    setConfirmRemove('');
    void act(id, localModelsApi.remove);
  };

  const example = models?.find((m) => m.installed && m.kind === 'decision');

  return (
    <div className={styles.page}>
      <header className={styles.pageHeader}>
        <h1 className={styles.title}>{t('local_models.title')}</h1>
        <p className={styles.description}>{t('local_models.description')}</p>
      </header>

      {error ? <div className={styles.errorBox}>{error}</div> : null}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('local_models.column.model')}</TableHead>
            <TableHead>{t('local_models.column.provider')}</TableHead>
            <TableHead>{t('local_models.column.size')}</TableHead>
            <TableHead>{t('local_models.column.status')}</TableHead>
            <TableHead alignRight />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(models ?? []).map((model) => (
            <ModelRow
              key={model.id}
              model={model}
              busy={busyId === model.id}
              onInstall={(id) => void act(id, localModelsApi.install)}
              onRemove={setConfirmRemove}
            />
          ))}
        </TableBody>
      </Table>

      <Modal
        open={confirmRemove !== ''}
        onClose={() => setConfirmRemove('')}
        width={420}
        title={t('local_models.remove_title', { model: confirmRemove })}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmRemove('')}>
              {t('common.cancel')}
            </Button>
            <Button variant="danger" onClick={remove}>
              {t('local_models.remove')}
            </Button>
          </>
        }
      >
        <p className={styles.description}>{t('local_models.remove_body')}</p>
      </Modal>

      {example ? (
        <section className={styles.usage}>
          <h2 className={styles.sectionTitle}>{t('local_models.usage_title')}</h2>
          <p className={styles.description}>{t('local_models.usage_description')}</p>
          <pre className={styles.code}>{systemOneExample(window.location.origin, example.id)}</pre>
        </section>
      ) : null}
    </div>
  );
}
