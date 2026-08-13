import { useCallback, useEffect, useState } from 'react';
import { brandingApi } from '@/lib/api';

const DEFAULT_NAME = 'MTProxyL-Panel';

/**
 * Имя панели: в шапке и во вкладке браузера.
 *
 * Заведено ради тех, у кого панелей несколько: одинаковые вкладки не
 * различить, а искать нужную перебором — то ещё занятие. Имя читается без
 * авторизации, потому что страница входа — такая же вкладка.
 *
 * Хранится на сервере, но кэшируется в localStorage: заголовок рисуется до
 * первого ответа сети, и без кэша он на мгновение показывал бы чужое имя.
 */
export function useBranding() {
  const [name, setName] = useState(() => {
    try {
      return localStorage.getItem('panel-name') || DEFAULT_NAME;
    } catch {
      return DEFAULT_NAME;
    }
  });

  const apply = useCallback((value: string) => {
    const next = value.trim() || DEFAULT_NAME;
    setName(next);
    document.title = next;
    try {
      localStorage.setItem('panel-name', next);
    } catch {
      // Приватный режим браузера — не повод ломать интерфейс.
    }
  }, []);

  useEffect(() => {
    document.title = name;
    void brandingApi
      .get()
      .then((res) => apply(res.name))
      .catch(() => undefined);
    // Один раз при монтировании: дальше имя меняет только форма настроек.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { name, apply };
}
