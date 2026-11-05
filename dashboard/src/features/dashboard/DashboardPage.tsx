import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { formatPercent } from '@/utils/format';
import { countReady } from './accounts';
import { AccountBoard } from './components/AccountBoard';
import { ThroughputChart } from './components/ThroughputChart';
import { useDashboardOverview } from './hooks/useDashboardOverview';
import { splitWindowMinutes } from './utils';
import styles from './dashboard.module.scss';

const DASH = '—';

export function DashboardPage() {
  const { t } = useTranslation();
  const serverVersion = useAuthStore((state) => state.serverVersion);
  const {
    connectionStatus,
    connected,
    config,
    counts,
    traffic,
    accounts,
    accountsLoaded,
    refresh,
  } = useDashboardOverview();

  useHeaderRefresh(refresh, connected);

  const windowLabel = useMemo(() => {
    if (traffic.windowMinutes <= 0) return t('dashboard.window_fallback');
    const { hours, minutes } = splitWindowMinutes(traffic.windowMinutes);
    if (hours === 0) return t('dashboard.window_m', { minutes });
    if (minutes === 0) return t('dashboard.window_h', { hours });
    return t('dashboard.window_hm', { hours, minutes });
  }, [traffic.windowMinutes, t]);

  const title = useMemo(() => {
    if (connectionStatus === 'connecting') return t('dashboard.title_connecting');
    if (!connected) return t('dashboard.title_offline');
    if (accounts.length === 0) return accountsLoaded ? t('dashboard.title_none') : '';
    return t('dashboard.title_ready', { ready: countReady(accounts), total: accounts.length });
  }, [accounts, accountsLoaded, connected, connectionStatus, t]);

  const summary = useMemo(() => {
    if (!connected) return t('dashboard.summary_offline');
    if (traffic.total === 0 || traffic.successRate === null) {
      return t('dashboard.summary_idle', { window: windowLabel });
    }
    return t('dashboard.summary_traffic', {
      total: traffic.total.toLocaleString(),
      window: windowLabel,
      rate: formatPercent(traffic.successRate),
    });
  }, [connected, traffic.successRate, traffic.total, t, windowLabel]);

  const setupRows = [
    { label: t('dashboard.runtime_routing'), value: config?.routingStrategy?.trim() || DASH },
    { label: t('dashboard.runtime_retry'), value: String(config?.requestRetry ?? 0) },
    { label: t('dashboard.setup_models'), value: counts.models?.toLocaleString() ?? DASH },
    { label: t('dashboard.setup_api_keys'), value: counts.providerKeys?.toLocaleString() ?? DASH },
    { label: t('dashboard.runtime_version'), value: serverVersion?.trim() || DASH },
  ];

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>{title}</h1>
          <p className={styles.summary}>{summary}</p>
        </div>
        <Link to="/add-account" className={styles.addAccount}>
          {t('dashboard.add_account')}
        </Link>
      </header>

      <section aria-label={t('dashboard.accounts_heading')}>
        {accounts.length > 0 ? (
          <AccountBoard accounts={accounts} />
        ) : (
          accountsLoaded && <p className={styles.empty}>{t('dashboard.accounts_empty')}</p>
        )}
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>{t('dashboard.traffic_title')}</h2>
        <p className={styles.sectionNote}>
          {t('dashboard.traffic_description', { window: windowLabel })}
        </p>
        <ThroughputChart traffic={traffic} />
      </section>

      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>{t('dashboard.setup_title')}</h2>
        <dl className={styles.setup}>
          {setupRows.map((row) => (
            <div key={row.label} className={styles.setupRow}>
              <dt>{row.label}</dt>
              <dd>{row.value}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  );
}
