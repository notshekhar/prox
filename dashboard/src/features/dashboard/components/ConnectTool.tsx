import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { useAuthStore } from '@/stores';
import { copyToClipboard } from '@/utils/clipboard';
import { maskApiKey } from '@/utils/format';
import styles from '../dashboard.module.scss';

const COPIED_RESET_MS = 1500;

interface ConnectLineProps {
  shown: string;
  copied: string;
}

function ConnectLine({ shown, copied }: ConnectLineProps) {
  const { t } = useTranslation();
  const [justCopied, setJustCopied] = useState(false);

  const copy = async () => {
    if (!(await copyToClipboard(copied))) return;
    setJustCopied(true);
    window.setTimeout(() => setJustCopied(false), COPIED_RESET_MS);
  };

  return (
    <div className={styles.connectLine}>
      <code>{shown}</code>
      <Button variant="ghost" size="sm" onClick={() => void copy()}>
        {justCopied ? t('dashboard.connect_copied') : t('dashboard.connect_copy')}
      </Button>
    </div>
  );
}

/** The two values a tool needs, so nobody has to find the client key in Settings. */
export function ConnectTool({ clientKey }: { clientKey?: string }) {
  const { t } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const baseLine = `export ANTHROPIC_BASE_URL=${apiBase}`;

  return (
    <section className={styles.section}>
      <h2 className={styles.sectionTitle}>{t('dashboard.connect_title')}</h2>
      <p className={styles.sectionNote}>{t('dashboard.connect_note')}</p>
      <div className={styles.connectLines}>
        <ConnectLine shown={baseLine} copied={baseLine} />
        {clientKey ? (
          <ConnectLine
            shown={`export ANTHROPIC_AUTH_TOKEN=${maskApiKey(clientKey)}`}
            copied={`export ANTHROPIC_AUTH_TOKEN=${clientKey}`}
          />
        ) : (
          <p className={styles.sectionNote}>{t('dashboard.connect_no_key')}</p>
        )}
      </div>
    </section>
  );
}
