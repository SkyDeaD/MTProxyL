#!/bin/sh
# Переходник на install-alertbot.sh.
#
# Ссылку на этот путь уже разослали, поэтому он остаётся рабочим. Вся логика
# переехала в install-alertbot.sh: прежний вариант звал подкоманду форка и на
# официальном MTProxyL не работал вовсе.
set -eu

BASE="${ALERTBOT_INSTALLER_BASE:-https://raw.githubusercontent.com/SkyDeaD/MTProxyL/tg-testing}"

printf '  Установщик переехал: install-alertbot.sh\n'
exec curl -fsSL "${BASE}/install-alertbot.sh" | sh -s -- "$@"
