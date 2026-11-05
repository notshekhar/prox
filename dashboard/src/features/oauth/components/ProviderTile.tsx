import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { ProviderFlowState } from '../hooks/useOAuthFlows';
import { BrandGlyph } from './BrandGlyph';
import styles from './ProviderTile.module.scss';

export interface ProviderTileProps {
  label: string;
  caption: string;
  icon: string;
  status?: ProviderFlowState['status'];
  statusLabel?: string;
  onOpen: () => void;
}

/** One provider per row; the whole row starts its sign-in. */
export function ProviderTile({
  label,
  caption,
  icon,
  status,
  statusLabel,
  onOpen,
}: ProviderTileProps) {
  const captionId = useId();
  const { t } = useTranslation();

  return (
    <div className={styles.tile} data-status={status ?? 'idle'}>
      <span className={styles.glyph}>
        <BrandGlyph src={icon} size={16} className={styles.glyphImage} />
      </span>
      <span className={styles.text}>
        <button
          type="button"
          className={styles.main}
          onClick={onOpen}
          aria-haspopup="dialog"
          aria-describedby={captionId}
        >
          {label}
        </button>
        <span className={styles.caption} id={captionId}>
          {status && statusLabel ? (
            <span className={styles.status}>
              <span className={styles.statusDot} aria-hidden="true" />
              {statusLabel}
            </span>
          ) : (
            <span className={styles.captionText}>{caption}</span>
          )}
        </span>
      </span>
      <span className={styles.trail} aria-hidden="true">
        {t('oauth_page.add')}
      </span>
    </div>
  );
}
