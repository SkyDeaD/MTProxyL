import { useEffect, useSyncExternalStore } from 'react';
import { brandingApi } from '@/lib/api';

const DEFAULT_NAME = 'MTProxyL-Panel';
const CACHE_KEY = 'panel-name';

/**
 * Имя панели: в шапке, на странице входа и во вкладке браузера.
 *
 * Заведено ради тех, у кого панелей несколько: одинаковые вкладки не
 * различить, а искать нужную перебором — то ещё занятие.
 *
 * Состояние общее на всё приложение, а не по копии на компонент. Иначе форма
 * сохранения меняет только собственную копию, шапка остаётся со старым именем,
 * и человек видит, что «кнопка не сработала», — пока не перезагрузит страницу.
 */
let current = readCache();
let requested = false;
const listeners = new Set<() => void>();

function readCache(): string {
  try {
    return localStorage.getItem(CACHE_KEY) || DEFAULT_NAME;
  } catch {
    // Приватный режим браузера — не повод ломать интерфейс.
    return DEFAULT_NAME;
  }
}

function writeCache(value: string) {
  try {
    localStorage.setItem(CACHE_KEY, value);
  } catch {
    // См. выше: кэш — удобство, а не условие работы.
  }
}

function subscribe(onChange: () => void) {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

function getSnapshot() {
  return current;
}

/** apply меняет имя разом везде: в шапке, во вкладке и в кэше. */
function apply(value: string) {
  const next = value.trim() || DEFAULT_NAME;
  document.title = next;
  writeCache(next);
  if (next === current) {
    return;
  }
  current = next;
  listeners.forEach((notify) => notify());
}

export function useBranding() {
  const name = useSyncExternalStore(subscribe, getSnapshot);

  useEffect(() => {
    document.title = current;
    // Спрашиваем сервер один раз на всё приложение: хук зовут четыре
    // компонента, и четыре одинаковых запроса подряд ни к чему.
    if (requested) {
      return;
    }
    requested = true;
    void brandingApi
      .get()
      .then((res) => apply(res.name))
      .catch(() => {
        // Не ответил — остаёмся на кэше: имя в шапке важнее правды о нём.
        requested = false;
      });
  }, []);

  return { name, apply };
}
