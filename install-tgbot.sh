#!/bin/sh
# Установка бота-сторожа поверх работающего MTProxyL.
#
# Сторож живёт отдельной службой и панель ему не нужна — но ставить его руками
# незачем: всё, что делает этот скрипт, умеет сам MTProxyL начиная с этой
# сборки. Скрипт остаётся точкой входа «одной командой» и просто зовёт CLI.
set -eu

SCRIPT="${MTPROXYL_SCRIPT:-/opt/mtproxyl/mtproxyl.sh}"

say() { printf '%s\n' "$*"; }
die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "нужны права root: запустите через sudo"
[ -x "$SCRIPT" ] || die "MTProxyL не найден по пути $SCRIPT"

if [ "${1:-}" = "--rollback" ] || [ "${1:-}" = "--uninstall" ]; then
    "$SCRIPT" alertbot uninstall --yes
    exit 0
fi

# Сторож умеет читать вердикт доступности только у MTProxyL 1.4.8 и новее:
# до него проверку вела панель, и вердикта в скрипте просто нет.
if ! "$SCRIPT" alertbot status >/dev/null 2>&1; then
    die "установленный MTProxyL про сторожа не знает — обновите его: mtproxyl update"
fi

TOKEN="${TOKEN:-}"
CHAT="${CHAT:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --token) TOKEN="$2"; shift 2 ;;
        --chat) CHAT="$2"; shift 2 ;;
        *) shift ;;
    esac
done

if [ -z "$TOKEN" ]; then
    say "Токен даёт @BotFather: /newbot, затем /token."
    printf 'Токен бота: '
    read -r TOKEN
fi
if [ -z "$CHAT" ]; then
    say "ID чата: свой узнаете у @userinfobot. Можно указать группу или канал."
    printf 'ID чата: '
    read -r CHAT
fi

[ -n "$TOKEN" ] || die "токен не задан"
[ -n "$CHAT" ] || die "ID чата не задан"

"$SCRIPT" alertbot install --token "$TOKEN" --chat "$CHAT"
say ""
say "Готово. Состояние: mtproxyl alertbot status"
say "Журнал:  journalctl -u mtproxyl-alertbot -f"
