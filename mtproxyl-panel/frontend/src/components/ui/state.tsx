import React from 'react';
import { cn } from '@/lib/utils';

interface EmptyStateProps {
  /** Что именно пусто — одной строкой, без «извините». */
  children: React.ReactNode;
  /** Что с этим делать: кнопка или ссылка. Необязательно. */
  action?: React.ReactNode;
  icon?: React.ReactNode;
  className?: string;
}

/**
 * Пусто — это не ошибка, поэтому серым и по центру, без рамок и значков
 * тревоги. Раньше каждая страница писала своё: где «Нет данных», где
 * «пока нет», где вовсе пустое место без объяснения.
 */
export function EmptyState({ children, action, icon, className }: EmptyStateProps) {
  return (
    <div className={cn('py-8 px-card text-center text-sm text-text-secondary', className)}>
      {icon && <div className="mb-2 flex justify-center text-text-secondary/60">{icon}</div>}
      <div>{children}</div>
      {action && <div className="mt-3 flex justify-center">{action}</div>}
    </div>
  );
}

/**
 * Заглушка на время загрузки. Показывает форму будущего содержимого, а не
 * слово «Loading»: так меньше скачет вёрстка, когда данные приходят.
 */
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      className={cn('animate-breathe rounded-md bg-surface-hover', className)}
      aria-hidden="true"
    />
  );
}

/** Несколько строк-заглушек подряд — типовой случай для списков и таблиц. */
export function SkeletonRows({ rows = 3, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn('space-y-2', className)}>
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-8 w-full" />
      ))}
    </div>
  );
}
