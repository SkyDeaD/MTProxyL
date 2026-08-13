import { useState } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { StatusDot } from '@/components/StatusDot';
import { alertbotApi, tgbotApi, type AlertbotStatus, type TgbotStatus } from '@/lib/api';
import { cn } from '@/lib/utils';

type Variant = 'admin' | 'alert';

interface Props {
  admin: TgbotStatus | null;
  alert: AlertbotStatus | null;
  /** Позвать после переключения: оба статуса надо перечитать, изменились обе службы. */
  onChanged: () => void | Promise<void>;
}

const LABELS: Record<Variant, { title: string; about: string }> = {
  admin: {
    title: 'Бот-администратор',
    about: 'управление прокси кнопками в чате: пользователи, ссылки, трафик, бэкапы',
  },
  alert: {
    title: 'Бот-сторож',
    about: 'тревоги: доступность из России и связь прокси с дата-центрами Telegram',
  },
};

/**
 * Выбор работающего бота.
 *
 * Отдельной карточкой, а не вкладками: вкладки читаются как «можно открыть обе»,
 * а тут выбор взаимоисключающий — включение одного останавливает другого. То же
 * решение, что на странице «Режим работы», и по той же причине.
 */
export function ActiveBotCard({ admin, alert, onChanged }: Props) {
  const [pending, setPending] = useState<Variant | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Активен тот, чья служба работает. Не то, что записано в настройках:
  // службу могли остановить руками, и врать об этом нельзя.
  const active: Variant | null = alert?.active ? 'alert' : admin?.active ? 'admin' : null;

  const state = (v: Variant) => (v === 'admin' ? admin : alert);
  const installed = (v: Variant) => state(v)?.installed ?? false;

  const label = (v: Variant) => {
    const st = state(v);
    if (!st?.installed) return 'не установлен';
    if (!st.configured) return 'установлен, не настроен';
    if (st.active) return 'работает';
    return 'остановлен';
  };

  const apply = async (v: Variant) => {
    setBusy(true);
    setError(null);
    try {
      await (v === 'alert' ? alertbotApi.activate() : tgbotApi.activate());
      await onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Не удалось переключить');
    } finally {
      setBusy(false);
      setPending(null);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Активный бот</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {(['admin', 'alert'] as Variant[]).map((v) => {
          const isActive = active === v;
          return (
            <div
              key={v}
              className={cn(
                'flex flex-wrap items-center gap-3 rounded-lg border p-3',
                isActive ? 'border-accent bg-accent/5' : 'border-border',
              )}
            >
              <StatusDot status={isActive ? 'ok' : installed(v) ? 'warn' : 'error'} />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{LABELS[v].title}</div>
                <div className="text-xs text-text-secondary">{LABELS[v].about}</div>
              </div>
              <span className="text-xs text-text-secondary">{label(v)}</span>
              {isActive ? (
                <span className="rounded-full bg-accent/15 px-2 py-0.5 text-xs font-medium text-accent">
                  активен
                </span>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || !installed(v)}
                  onClick={() => setPending(v)}
                >
                  Сделать активным
                </Button>
              )}
            </div>
          );
        })}

        <p className="text-xs text-text-secondary">
          Работать может только один: включение одного останавливает другого. Настройки и токен
          второго при этом остаются на месте.
        </p>

        {error && <p className="text-xs text-danger">{error}</p>}
      </CardContent>

      <ConfirmDialog
        open={pending !== null}
        title={pending ? `Включить ${LABELS[pending].title.toLowerCase()}?` : ''}
        message={
          pending
            ? `${LABELS[pending].title} будет запущен и добавлен в автозапуск, а второй бот — остановлен. Его настройки и токен сохранятся.`
            : ''
        }
        confirmLabel="Переключить"
        confirmVariant="default"
        loading={busy}
        onClose={() => setPending(null)}
        onConfirm={() => pending && void apply(pending)}
      />
    </Card>
  );
}
