import React from 'react';
import { cn } from '@/lib/utils';

interface SectionTitleProps extends React.HTMLAttributes<HTMLHeadingElement> {
  /** Пояснение под заголовком — тем же кеглем, что и описания страниц. */
  description?: React.ReactNode;
  /** Кнопки секции: уходят вправо, в одну строку с заголовком. */
  actions?: React.ReactNode;
}

/**
 * Заголовок секции внутри страницы.
 *
 * Заведён потому, что таких заголовков было четыре системы разом: серым
 * мелким, мелким жирным, крупным и «как в карточке» — и отступ снизу
 * вразнобой, от нуля до 16px. Секция, оформленная через Card, и секция,
 * собранная руками, выглядели как из разных приложений.
 *
 * Кегль и вес совпадают с CardTitle: это один и тот же уровень заголовка,
 * и различаться им не за что.
 */
export function SectionTitle({
  className,
  description,
  actions,
  children,
  ...props
}: SectionTitleProps) {
  return (
    <div className={cn('flex items-start justify-between gap-3 mb-3', className)}>
      <div className="min-w-0">
        <h2
          className="section-title"
          {...props}
        >
          {children}
        </h2>
        {description && (
          <p className="text-xs text-text-secondary mt-1">{description}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
    </div>
  );
}
