import { useTranslation } from 'react-i18next';
import { formatPercent } from '@/utils/format';
import type { AccountRow, AccountState } from '../accounts';
import { providerLabel } from '../utils';
import { Sparkline } from './Sparkline';
import styles from './AccountBoard.module.scss';

const LAMP_CLASS: Record<AccountState, string> = {
  ready: styles.lampReady,
  off: styles.lampOff,
  problem: styles.lampProblem,
};

interface AccountBoardProps {
  accounts: AccountRow[];
}

/** Every account in one table, with the ones that need attention first. */
export function AccountBoard({ accounts }: AccountBoardProps) {
  const { t } = useTranslation();
  const unknownProvider = t('dashboard.provider_unknown');

  return (
    <div className={styles.board}>
      <div className={styles.columns} aria-hidden="true">
        <span>{t('dashboard.column_account')}</span>
        <span>{t('dashboard.column_activity')}</span>
        <span className={styles.number}>{t('dashboard.column_requests')}</span>
        <span className={styles.number}>{t('dashboard.column_success')}</span>
      </div>
      <ul className={styles.rows}>
        {accounts.map((account) => (
          <li key={account.id} className={styles.row}>
            <span
              className={`${styles.lamp} ${LAMP_CLASS[account.state]}`}
              role="img"
              aria-label={t(`dashboard.state_${account.state}`)}
              title={t(`dashboard.state_${account.state}`)}
            />
            <div className={styles.identity}>
              <span className={styles.name}>{account.label}</span>
              <span className={styles.meta}>
                {providerLabel(account.provider, unknownProvider)}
                {account.state !== 'ready' && `, ${t(`dashboard.state_${account.state}`)}`}
              </span>
              {account.note && <span className={styles.note}>{account.note}</span>}
            </div>
            {account.total > 0 ? (
              <Sparkline
                points={account.activity}
                color="var(--text-tertiary)"
                ariaLabel={t('dashboard.activity_label', { account: account.label })}
                className={styles.activity}
              />
            ) : (
              <span className={styles.idle}>{t('dashboard.activity_none')}</span>
            )}
            <span className={styles.number}>{account.total.toLocaleString()}</span>
            <span className={styles.number}>
              {account.successRate === null ? '—' : formatPercent(account.successRate)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
