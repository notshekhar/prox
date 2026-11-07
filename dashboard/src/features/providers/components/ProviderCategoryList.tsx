import { useTranslation } from 'react-i18next';
import { PROVIDER_LOGOS } from '../brandLogos';
import type { ProviderBrand, ProviderGroup } from '../types';
import styles from './ProviderCategoryList.module.scss';

interface ProviderCategoryListProps {
  groups: ProviderGroup[];
  activeBrand: ProviderBrand;
  onSelect: (brand: ProviderBrand) => void;
}

interface ProviderRowProps {
  group: ProviderGroup;
  active: boolean;
  onSelect: (brand: ProviderBrand) => void;
}

const QUICK_FILL_BRAND_ORDER: readonly ProviderBrand[] = ['fennoAI', 'qiniuCloud', 'aiCodeMirror'];

const QUICK_FILL_BRANDS: ReadonlySet<ProviderBrand> = new Set(QUICK_FILL_BRAND_ORDER);

const joinClasses = (...names: Array<string | false | undefined>) =>
  names.filter(Boolean).join(' ');

function ProviderLogo({ brand }: { brand: ProviderBrand }) {
  const logo = PROVIDER_LOGOS[brand];
  if (!logo) return null;

  const surfaceClasses = joinClasses(
    styles.logo,
    logo.transparent && styles.logoTransparent,
    logo.themeSurface && styles.logoThemeSurface
  );
  return (
    <>
      <img
        src={logo.src}
        alt=""
        aria-hidden="true"
        className={joinClasses(
          surfaceClasses,
          logo.darkSrc && styles.logoThemeLight,
          logo.invertOnDark && styles.logoInvertOnDark
        )}
      />
      {logo.darkSrc ? (
        <img
          src={logo.darkSrc}
          alt=""
          aria-hidden="true"
          className={joinClasses(surfaceClasses, styles.logoThemeDark)}
        />
      ) : null}
    </>
  );
}

function ProviderRow({ group, active, onSelect }: ProviderRowProps) {
  const { t } = useTranslation();
  const total = group.resources.length;
  const enabled = group.resources.filter((resource) => !resource.disabled).length;

  return (
    <button
      type="button"
      className={joinClasses(styles.item, active && styles.active)}
      onClick={() => onSelect(group.id)}
      aria-current={active ? 'page' : undefined}
    >
      <ProviderLogo brand={group.id} />
      <span className={styles.itemTitle}>{t(`providersPage.providerNames.${group.id}`)}</span>
      {total > 0 ? (
        <span
          className={styles.count}
          title={t('providersPage.categories.activeCount', { active: enabled, total })}
        >
          {enabled === total ? total : `${enabled}/${total}`}
        </span>
      ) : null}
    </button>
  );
}

export function ProviderCategoryList({ groups, activeBrand, onSelect }: ProviderCategoryListProps) {
  const { t } = useTranslation();

  const quickFillGroups = groups
    .filter((group) => QUICK_FILL_BRANDS.has(group.id))
    .sort(
      (left, right) =>
        QUICK_FILL_BRAND_ORDER.indexOf(left.id) - QUICK_FILL_BRAND_ORDER.indexOf(right.id)
    );
  const providerGroups = groups.filter((group) => !QUICK_FILL_BRANDS.has(group.id));
  const sections = [
    {
      label: t('providersPage.categories.inUse'),
      items: providerGroups.filter((group) => group.resources.length > 0),
    },
    {
      label: t('providersPage.categories.available'),
      items: providerGroups.filter((group) => group.resources.length === 0),
    },
    { label: t('providersPage.categories.quickFill'), items: quickFillGroups },
  ].filter((section) => section.items.length > 0);

  return (
    <nav className={styles.rail}>
      {sections.map((section) => (
        <section key={section.label}>
          <p className={styles.groupLabel}>{section.label}</p>
          <div className={styles.list}>
            {section.items.map((group) => (
              <ProviderRow
                key={group.id}
                group={group}
                active={group.id === activeBrand}
                onSelect={onSelect}
              />
            ))}
          </div>
        </section>
      ))}
    </nav>
  );
}
