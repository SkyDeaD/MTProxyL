import React from 'react';
import { RefreshCw } from 'lucide-react';
import { cn } from '@/lib/utils';

interface PageShellProps {
  /** Заголовок раздела. */
  title: string;
  /** Пояснение под заголовком: зачем раздел и что он меняет. */
  description?: React.ReactNode;
  /** Кнопки раздела — уходят вправо, в одну строку с заголовком. */
  actions?: React.ReactNode;
  refreshing?: boolean;
  onRefresh?: () => void;
  /**
   * Страница занимает высоту окна и прокручивается внутри себя — так живут
   * логи и редактор конфига. Обычным страницам это не нужно: они растут вниз.
   */
  fullHeight?: boolean;
  className?: string;
  children: React.ReactNode;
}

/**
 * Оболочка страницы: заголовок, отступ и вертикальный ритм в одном месте.
 *
 * Заведена потому, что страниц было две породы. Одни рисовали <Header> и
 * клали содержимое в блок с отступом, другие — собственный <h1> и вовсе без
 * отступа, потому что каркас отдаёт <Outlet /> голым. Пересечения между
 * породами не было ни одного, и разнобой было видно при первом же переходе
 * между разделами.
 *
 * Всё, что задаёт ритм страницы, живёт здесь: менять отступы теперь надо в
 * одном файле, а не в двадцати шести.
 */
export function PageShell({
  title,
  description,
  actions,
  refreshing,
  onRefresh,
  fullHeight,
  className,
  children,
}: PageShellProps) {
  return (
    <div
      className={cn(
        'p-4 lg:p-6',
        // Высоту считаем от окна: на телефоне снизу висит своя навигация,
        // поэтому вычитаем её вместе с шапкой-гамбургером.
        fullHeight && 'flex flex-col h-[calc(100dvh-7.5rem)] lg:h-dvh',
      )}
    >
      <div className="flex items-start justify-between gap-4 flex-wrap mb-4">
        <div className="min-w-0">
          <h1 className="text-xl font-[640] tracking-[-0.02em] text-text-primary">{title}</h1>
          {description && (
            <p className="text-xs text-text-secondary mt-1 max-w-3xl">{description}</p>
          )}
        </div>

        {(actions || onRefresh) && (
          <div className="flex items-center gap-2 shrink-0">
            {actions}
            {onRefresh && (
              <button
                onClick={onRefresh}
                className="p-2 rounded-md text-text-secondary hover:text-text-primary hover:bg-surface-hover transition-colors"
                title="Обновить"
              >
                <RefreshCw size={16} className={cn(refreshing && 'animate-spin')} />
              </button>
            )}
          </div>
        )}
      </div>

      <div className={cn('space-y-4', fullHeight && 'flex-1 min-h-0 flex flex-col', className)}>
        {children}
      </div>
    </div>
  );
}
