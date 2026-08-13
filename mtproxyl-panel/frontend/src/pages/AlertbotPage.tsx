import { useCallback, useEffect, useState } from 'react';
import { Play, RefreshCw, Square, Trash2 } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ErrorAlert } from '@/components/ErrorAlert';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { OperationProgress } from '@/components/OperationProgress';
import { StatusDot } from '@/components/StatusDot';
import { useMtproxylOperation } from '@/hooks/useMtproxyl';
import { alertbotApi, type AlertbotStatus } from '@/lib/api';

/**
 * Бот-сторож: только наблюдение, зато в обе стороны. Доступность из России —
 * это вход, её считает сам MTProxyL; связь с дата-центрами Telegram — выход,
 * её бот снимает у движка. Второе и есть причина, по которой он существует:
 * фильтрация чаще режет выход, и тогда зонды показывают 95%, а клиенты не
 * работают.
 *
 * Управление тем же CLI, что и у бота-администратора: панель показывает
 * состояние и нажимает кнопки.
 */
export function AlertbotPanel({ onChanged }: { onChanged?: () => void | Promise<void> } = {}) {
  const [status, setStatus] = useState<AlertbotStatus | null>(null);
  const [supported, setSupported] = useState(true);
  const [message, setMessage] = useState('');
  const [hint, setHint] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [logs, setLogs] = useState('');
  const [confirmRemove, setConfirmRemove] = useState(false);

  const [token, setToken] = useState('');
  const [chat, setChat] = useState('');

  const load = useCallback(async () => {
    try {
      const res = await alertbotApi.status();
      setSupported(res.supported);
      setMessage(res.message ?? '');
      setHint(res.hint ?? '');
      setStatus(res.status ?? null);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Не удалось получить состояние');
    }
  }, []);

  const { operation, dismiss } = useMtproxylOperation(load, ['alertbot:']);

  useEffect(() => {
    void load();
  }, [load]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      await load();
      // Действия сторожа задевают и второго бота — страница сверху обязана
      // это увидеть, иначе карточка выбора покажет вчерашнюю картину.
      await onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Не удалось выполнить действие');
    } finally {
      setBusy(false);
    }
  };

  const showLogs = async () => {
    try {
      const res = await alertbotApi.logs(120);
      setLogs(res.lines || 'Журнал пуст');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Журнал недоступен');
    }
  };

  if (!supported) {
    return (
      <Card>
        <CardContent className="py-6 text-sm text-text-secondary">{message}</CardContent>
      </Card>
    );
  }

  const installed = status?.installed ?? false;
  const configured = status?.configured ?? false;
  const serviceUp = status?.active ?? false;
  const cfg = status?.config;
  // Пустые поля — «переустановить с прежним токеном»; это возможно, только
  // если токен уже сохранён.
  const canInstall = configured || (token.trim() !== '' && chat.trim() !== '');

  return (
    <div className="space-y-4">
      {error && <ErrorAlert message={error} onRetry={() => void load()} />}

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Состояние</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => void load()} className="gap-2">
            <RefreshCw className="h-4 w-4" /> Обновить
          </Button>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="flex items-center gap-2">
            <StatusDot status={!installed ? 'error' : serviceUp ? 'ok' : 'warn'} />
            <span className="font-medium">
              {!installed
                ? 'Не установлен'
                : !configured
                  ? 'Установлен, но не настроен'
                  : serviceUp
                    ? 'Работает'
                    : 'Остановлен'}
            </span>
            {installed && !status?.enabled && (
              <span className="text-xs text-text-secondary">автозапуск выключен</span>
            )}
          </div>

          {installed && (
            <div className="grid gap-1 text-xs text-text-secondary">
              <div>Каталог: {status?.dir}</div>
              <div>Служба: {status?.service}</div>
              {cfg?.chat_id ? <div>Чат: {cfg.chat_id}</div> : null}
            </div>
          )}

          {installed && (
            <div className="flex flex-wrap gap-2 pt-1">
              <Button
                size="sm"
                disabled={busy}
                className="gap-2"
                onClick={() => void act(() => alertbotApi.service(serviceUp ? 'restart' : 'start'))}
              >
                {serviceUp ? <RefreshCw className="h-4 w-4" /> : <Play className="h-4 w-4" />}
                {serviceUp ? 'Перезапустить' : 'Запустить'}
              </Button>
              {serviceUp && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  className="gap-2"
                  onClick={() => void act(() => alertbotApi.service('stop'))}
                >
                  <Square className="h-4 w-4" /> Остановить
                </Button>
              )}
              {!serviceUp && configured && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() => void act(() => alertbotApi.activate())}
                >
                  Сделать активным
                </Button>
              )}
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => void act(() => alertbotApi.service('update'))}
              >
                Обновить бота
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void showLogs()}>
                Журнал службы
              </Button>
              <Button
                size="sm"
                variant="danger"
                disabled={busy}
                className="gap-2"
                onClick={() => setConfirmRemove(true)}
              >
                <Trash2 className="h-4 w-4" /> Удалить
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {logs && (
        <Card>
          <CardHeader>
            <CardTitle>Журнал службы</CardTitle>
          </CardHeader>
          <CardContent>
            <pre className="max-h-96 overflow-auto rounded bg-surface-2 p-3 text-xs">{logs}</pre>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>{installed ? 'Переустановка' : 'Установка'}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {hint && <p className="text-xs text-text-secondary">{hint}</p>}
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="alertbot-token">Токен бота</Label>
              <Input
                id="alertbot-token"
                value={token}
                placeholder="1234567890:AAH…"
                onChange={(e) => setToken(e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="alertbot-chat">ID чата</Label>
              <Input
                id="alertbot-chat"
                value={chat}
                placeholder="123456789 или -1001234567890"
                onChange={(e) => setChat(e.target.value.replace(/[^\d-]/g, ''))}
              />
            </div>
          </div>
          <Button
            disabled={busy || !canInstall}
            onClick={() =>
              void act(async () => {
                await alertbotApi.install(token.trim(), Number(chat.trim() || 0));
                setToken('');
              })
            }
          >
            {installed ? 'Переустановить' : 'Установить бота'}
          </Button>
          <OperationProgress operation={operation} onDismiss={dismiss} />
        </CardContent>
      </Card>

      {configured && cfg && (
        <Card>
          <CardHeader>
            <CardTitle>Настройки</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <ValueRow
              label="Порог доступности, %"
              hint="Ниже него бот шлёт тревогу. Обычно это значит, что адрес попал под фильтр у части операторов."
              initial={String(cfg.alert_threshold || 60)}
              onSave={(v) => act(() => alertbotApi.set('alert_threshold', v))}
              busy={busy}
            />
            <ValueRow
              label="Порог отказов к дата-центрам, %"
              hint="Доля отказов без повтора при подключении к Telegram. На исправном сервере их ноль."
              initial={String(cfg.connect_fail_threshold || 20)}
              onSave={(v) => act(() => alertbotApi.set('connect_fail_threshold', v))}
              busy={busy}
            />
            <ValueRow
              label="Часовой пояс"
              hint="Пусто — определять самому. Нужен, если сервер и бот видят разное время: Asia/Tashkent, Europe/Moscow."
              initial={cfg.timezone}
              onSave={(v) => act(() => alertbotApi.set('timezone', v))}
              busy={busy}
              allowEmpty
            />
            <ValueRow
              label="ID чата"
              hint="Личка, группа или канал. При смене бот убирает свои сообщения в прежнем чате."
              initial={String(cfg.chat_id || '')}
              onSave={(v) => act(() => alertbotApi.set('chat_id', v))}
              busy={busy}
            />
          </CardContent>
        </Card>
      )}

      <ConfirmDialog
        open={confirmRemove}
        title="Удалить бота-сторожа?"
        message="Служба, каталог и права sudo будут удалены. Прокси и его настройки это не затрагивает."
        confirmLabel="Удалить"
        confirmVariant="danger"
        onClose={() => setConfirmRemove(false)}
        onConfirm={() => {
          setConfirmRemove(false);
          void act(() => alertbotApi.uninstall());
        }}
      />
    </div>
  );
}

function ValueRow({
  label,
  hint,
  initial,
  onSave,
  busy,
  allowEmpty = false,
}: {
  label: string;
  hint: string;
  initial: string;
  onSave: (value: string) => Promise<unknown>;
  busy: boolean;
  allowEmpty?: boolean;
}) {
  const [value, setValue] = useState(initial);
  useEffect(() => setValue(initial), [initial]);

  const changed = value !== initial;
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <div className="flex gap-2">
        <Input value={value} onChange={(e) => setValue(e.target.value)} />
        <Button
          size="sm"
          variant="outline"
          disabled={busy || !changed || (!allowEmpty && value.trim() === '')}
          onClick={() => void onSave(value.trim())}
        >
          Сохранить
        </Button>
      </div>
      <p className="text-xs text-text-secondary">{hint}</p>
    </div>
  );
}
