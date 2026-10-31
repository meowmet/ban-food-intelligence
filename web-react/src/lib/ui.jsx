import React, { useState } from 'react';
import { useLang } from './i18n';

/** Screen header: large title + one-line explanation + optional actions. */
export function Page({ title, subtitle, actions, children }) {
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>{title}</h1>
          {subtitle && <p className="page-sub">{subtitle}</p>}
        </div>
        {actions && <div className="page-actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

/** Inset group: white rounded block with an optional grey header and footer. */
export function Group({ title, footer, aside, children, className = '' }) {
  return (
    <div className={`group-wrap ${className}`}>
      {(title || aside) && (
        <div className="group-title">
          <span>{title}</span>
          {aside}
        </div>
      )}
      <div className="group">{children}</div>
      {footer && <p className="group-footer">{footer}</p>}
    </div>
  );
}

export function Kpi({ label, value, sub, tone }) {
  return (
    <div className="kpi">
      <div className="kpi-label">{label}</div>
      <div className={`kpi-value ${tone ? `tone-${tone}` : ''}`}>{value}</div>
      {sub && <div className="kpi-sub">{sub}</div>}
    </div>
  );
}

export function Pill({ tone = 'neutral', children }) {
  return <span className={`pill pill-${tone}`}>{children}</span>;
}

/**
 * Table that scrolls inside its group on narrow screens.
 * columns: [{ key, label, align: 'num'|'text', render?: row => node }]
 */
export function Table({ columns, rows, empty, footer, rowKey, rowTone }) {
  const { t } = useLang();
  if (!rows || rows.length === 0) return <Empty text={empty || t('noData')} />;
  return (
    <div className="table-scroll">
      <table className="table">
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key} className={c.align === 'num' ? 'num' : ''}>{c.label}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={rowKey ? rowKey(r, i) : i} className={rowTone ? `row-${rowTone(r) || ''}` : ''}>
              {columns.map((c) => (
                <td key={c.key} className={c.align === 'num' ? 'num' : ''}>{c.render ? c.render(r) : r[c.key]}</td>
              ))}
            </tr>
          ))}
        </tbody>
        {footer && <tfoot>{footer}</tfoot>}
      </table>
    </div>
  );
}

export function Loading({ rows = 4 }) {
  return (
    <div className="skeleton" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="skeleton-row" />
      ))}
    </div>
  );
}

export function Empty({ text }) {
  return <p className="empty">{text}</p>;
}

/** Error state: what happened (localised), what to do, technical details on demand. */
export function ErrorState({ error, onRetry }) {
  const { t } = useLang();
  const [open, setOpen] = useState(false);
  const message = error?.code === 'NETWORK' ? t('errorNetwork') : error?.message;
  return (
    <div className="error-state" role="alert">
      <p>{message}</p>
      <div className="error-actions">
        {onRetry && <button className="btn-text" onClick={onRetry}>{t('retry')}</button>}
        {error?.details && (
          <button className="btn-text muted" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
            {t('details')}
          </button>
        )}
      </div>
      {open && <pre className="error-details">{typeof error.details === 'string' ? error.details : JSON.stringify(error.details, null, 2)}</pre>}
    </div>
  );
}

/** Wraps a useApi() result: loading / error / content. */
export function Async({ result, children, rows }) {
  if (result.loading && !result.data) return <Loading rows={rows} />;
  if (result.error) return <ErrorState error={result.error} onRetry={result.reload} />;
  if (result.data == null) return <Loading rows={rows} />;
  return children(result.data);
}

export function Segmented({ options, value, onChange, label }) {
  return (
    <div className="segmented" role="tablist" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="tab"
          aria-selected={o.value === value}
          className={o.value === value ? 'active' : ''}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Mono({ children }) {
  return <span className="mono">{children}</span>;
}
