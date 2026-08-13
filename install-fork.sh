#!/bin/sh
# MTProxyL (форк SkyDeaD) — установка целиком, одной командой.
#
# Скрипт решает оба случая сам:
#
#   MTProxyL уже стоит  — обновляет скрипт и библиотеки до форка, не трогая
#                         настройки, секреты, движок и контейнер;
#   MTProxyL не стоит   — ставит его с нуля из форка и открывает мастер.
#
# Дальше — панель со страницей бота и сам бот-сторож. Отказаться от любого
# шага можно флагом: --no-panel, --no-bot.
#
#   curl -fsSL .../install-fork.sh | sudo sh
#   curl -fsSL .../install-fork.sh | sudo sh -s -- --token T --chat ID
set -eu

REPO="${MTPROXYL_REPO:-SkyDeaD/MTProxyL}"
BRANCH="${MTPROXYL_BRANCH:-tg-testing}"
RAW="https://raw.githubusercontent.com/${REPO}/${BRANCH}"
INSTALL_DIR="/opt/mtproxyl"

WITH_PANEL="yes"
WITH_BOT="yes"
TOKEN="${TOKEN:-}"
CHAT="${CHAT:-}"

say()  { printf '  %s\n' "$*"; }
ok()   { printf '  [+] %s\n' "$*"; }
warn() { printf '  [!] %s\n' "$*" >&2; }
die()  { printf '  [x] %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --token) TOKEN="$2"; shift 2 ;;
        --chat)  CHAT="$2";  shift 2 ;;
        --repo)  REPO="$2";  RAW="https://raw.githubusercontent.com/${REPO}/${BRANCH}"; shift 2 ;;
        --branch) BRANCH="$2"; RAW="https://raw.githubusercontent.com/${REPO}/${BRANCH}"; shift 2 ;;
        --no-panel) WITH_PANEL="no"; shift ;;
        --no-bot)   WITH_BOT="no";   shift ;;
        -h|--help)
            cat <<'USAGE'
  install-fork.sh [--token T --chat ID] [--no-panel] [--no-bot]
                  [--repo владелец/репозиторий] [--branch ветка]
USAGE
            exit 0 ;;
        *) shift ;;
    esac
done

[ "$(id -u)" = "0" ] || die "нужны права root: запустите через sudo"
command -v curl >/dev/null 2>&1 || die "нужна утилита curl"

# ── 1. MTProxyL ───────────────────────────────────────────────

fetch() {
    _url="$1" _dest="$2"
    _tmp=$(mktemp) || return 1
    if ! curl -fsSL "$_url" -o "$_tmp"; then
        rm -f "$_tmp"
        return 1
    fi
    # Оборванная закачка не должна попасть на место рабочего файла: половина
    # bash-скрипта — это не «немного сломано», это сломано целиком.
    if ! sh -n "$_tmp" 2>/dev/null && ! bash -n "$_tmp" 2>/dev/null; then
        rm -f "$_tmp"
        return 1
    fi
    install -m 0755 "$_tmp" "$_dest"
    rm -f "$_tmp"
}

if [ -x "${INSTALL_DIR}/mtproxyl.sh" ]; then
    say "MTProxyL уже установлен — обновляю его файлы до форка"
    say "Настройки, секреты, движок и контейнер не трогаются"

    fetch "${RAW}/mtproxyl.sh" "${INSTALL_DIR}/mtproxyl.sh" \
        || die "не удалось скачать mtproxyl.sh"

    # Список библиотек берём из только что скачанного скрипта — тем же
    # способом, что и его собственное обновление. Так список не приходится
    # держать в двух местах и он не разъезжается с кодом.
    _libs=$(sed -n 's/^for _lib in \([^;]*\); do.*/\1/p' "${INSTALL_DIR}/mtproxyl.sh" | head -1)
    [ -n "$_libs" ] || die "не удалось прочитать список библиотек"

    mkdir -p "${INSTALL_DIR}/lib"
    _failed=""
    for _lib in $_libs; do
        if ! fetch "${RAW}/lib/${_lib}.sh" "${INSTALL_DIR}/lib/${_lib}.sh"; then
            _failed="${_failed} ${_lib}"
        fi
    done
    [ -z "$_failed" ] || die "не скачались библиотеки:${_failed}"

    ln -sf "${INSTALL_DIR}/mtproxyl.sh" /usr/local/bin/mtproxyl
    ok "MTProxyL обновлён до форка"
else
    say "MTProxyL не найден — ставлю с нуля из форка"
    # Установщик MTProxyL написан на bash и пользуется его синтаксисом. Запуск
    # через sh на Debian и Ubuntu отдаёт его dash, где «command -v curl
    # &>/dev/null» читается как «запустить в фоне», — и установка заявляет, что
    # curl не найден, печатая рядом путь к нему же.
    command -v bash >/dev/null 2>&1 || die "нужен bash: установщик MTProxyL написан на нём (apt install bash)"

    _inst=$(mktemp)
    curl -fsSL "${RAW}/install.sh" -o "$_inst" || die "не удалось скачать install.sh"
    # Установка с нуля идёт мастером и спрашивает про порт, домен и прочее,
    # поэтому запускаем её с терминалом, а не из трубы.
    if [ -r /dev/tty ]; then
        bash "$_inst" --repo "$REPO" --branch "$BRANCH" < /dev/tty
    else
        bash "$_inst" --repo "$REPO" --branch "$BRANCH"
    fi
    rm -f "$_inst"
    exit 0
fi

# Обновления должны приходить оттуда же, откуда пришла установка: иначе первый
# же `mtproxyl update` вернёт файлы автора и унесёт всё, чего в них нет.
if [ "$REPO" = "Liafanx/MTProxyL" ]; then
    rm -f "${INSTALL_DIR}/.repo"
else
    printf '%s\n' "$REPO" > "${INSTALL_DIR}/.repo"
    chmod 600 "${INSTALL_DIR}/.repo"
    ok "Обновления будут приходить из ${REPO}"
fi
if [ "$BRANCH" = "main" ]; then
    rm -f "${INSTALL_DIR}/.branch"
else
    printf '%s\n' "$BRANCH" > "${INSTALL_DIR}/.branch"
    chmod 600 "${INSTALL_DIR}/.branch"
fi

# ── 2. Панель ─────────────────────────────────────────────────

if [ "$WITH_PANEL" = "yes" ] && [ -x /usr/local/bin/mtproxyl-panel ]; then
    say ""
    say "Обновляю панель до сборки форка"
    _pan=$(mktemp)
    _log=$(mktemp)
    if curl -fsSL "${RAW}/mtproxyl-panel/install.sh" -o "$_pan"; then
        if MTPROXYL_PANEL_REPO="$REPO" sh "$_pan" install >"$_log" 2>&1; then
            ok "Панель обновлена — в ней появился раздел бота-сторожа"
        else
            # Показываем хвост вывода: тихое «не удалось» на месте провала —
            # худшее, что может сделать установщик, человеку потом нечего
            # прислать в поддержку.
            warn "панель обновить не удалось, последние строки вывода:"
            tail -n 15 "$_log" | sed 's/^/      /' >&2
            warn "повторить: MTPROXYL_PANEL_REPO=${REPO} sh ${_pan} install"
        fi
    else
        warn "установщик панели не скачался"
    fi
    rm -f "$_pan" "$_log"
elif [ "$WITH_PANEL" = "yes" ]; then
    say ""
    say "Панель не установлена — пропускаю. Поставить: mtproxyl panel install"
fi

# ── 3. Бот-сторож ─────────────────────────────────────────────

# Бота ставим, только если человек этого хочет. Молча — когда он уже стоит
# (тогда это обновление) или когда токен передан флагами. Иначе спрашиваем
# один раз, с умолчанием «нет»: ровно так же ведёт себя первая установка
# MTProxyL со своим ботом, и токен от BotFather есть не у всех и не сразу.
if [ "$WITH_BOT" = "yes" ] && [ ! -s /opt/mtproxyl-alertbot/config.json ] && [ -z "$TOKEN" ]; then
    say ""
    say "Бот-сторож: одно живое сообщение в чате и звонок, когда прокси перестал"
    say "работать. Понадобится токен от BotFather. Поставить можно и позже —"
    say "из панели или пунктом меню «Телеграм бот»."
    printf '  Установить бота-сторожа? [y/N]: '
    _yn=""
    if [ -t 0 ]; then
        read -r _yn
    elif [ -r /dev/tty ]; then
        read -r _yn < /dev/tty
    else
        printf '\n'
    fi
    case "$_yn" in
        [yYдД]*) ;;
        *) WITH_BOT="no"; say "Пропускаю — поставите когда понадобится" ;;
    esac
fi

if [ "$WITH_BOT" = "yes" ]; then
    say ""
    _bot=$(mktemp)
    if curl -fsSL "${RAW}/install-alertbot.sh" -o "$_bot"; then
        # Панель уже обновлена выше — второй раз ей заниматься незачем.
        set -- --no-panel --repo "$REPO" --branch "$BRANCH"
        [ -n "$TOKEN" ] && set -- "$@" --token "$TOKEN"
        [ -n "$CHAT" ] && set -- "$@" --chat "$CHAT"
        sh "$_bot" "$@" || warn "бота-сторожа поставить не удалось"
    else
        warn "установщик бота не скачался"
    fi
    rm -f "$_bot"
fi

say ""
ok "Готово"
say "Панель:    раздел «Телеграм-бот» → переключатель между ботами"
say "Сторож:    /opt/mtproxyl-alertbot/mtproxyl-alertbot config show"
say ""

# Открываем меню, как это делает установка с нуля: человек только что поставил
# форк и хочет посмотреть, что получилось, а не набирать ещё одну команду.
# Скрипт пришёл по трубе, поэтому ввод сначала возвращаем на терминал.
if [ ! -t 0 ] && [ -r /dev/tty ]; then
    exec < /dev/tty || true
fi
if [ -t 0 ]; then
    exec /usr/local/bin/mtproxyl
fi
say "Меню:      mtproxyl"
