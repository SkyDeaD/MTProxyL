#!/bin/sh
# Бот-сторож для MTProxyL — установка поверх работающего прокси.
#
# Скрипт намеренно самодостаточен: он не зовёт подкоманды MTProxyL и не требует
# форкнутого mtproxyl.sh. Это важно, потому что иначе установка была бы
# возможна только на форк, а обновление автора однажды забрало бы подкоманду
# обратно. От MTProxyL нужно ровно то, что есть в официальной версии 1.4.8:
# три подкоманды `availability`, порт API движка в settings.conf и заголовок
# авторизации в конфиге движка.
#
#   curl -fsSL .../install-alertbot.sh | sh
#   curl -fsSL .../install-alertbot.sh | sh -s -- --uninstall
set -eu

REPO="${ALERTBOT_REPO:-SkyDeaD/MTProxyL}"
MTPROXYL_DIR="${MTPROXYL_DIR:-/opt/mtproxyl}"
SCRIPT="${MTPROXYL_SCRIPT:-${MTPROXYL_DIR}/mtproxyl.sh}"
SETTINGS="${MTPROXYL_SETTINGS:-${MTPROXYL_DIR}/settings.conf}"

DIR="/opt/mtproxyl-alertbot"
USER_NAME="mtproxyl-alertbot"
SERVICE="mtproxyl-alertbot.service"
SUDOERS="/etc/sudoers.d/mtproxyl-alertbot"
CONFIG="${DIR}/config.json"
BIN="${DIR}/mtproxyl-alertbot"

say()  { printf '  %s\n' "$*"; }
ok()   { printf '  [+] %s\n' "$*"; }
warn() { printf '  [!] %s\n' "$*" >&2; }
die()  { printf '  [x] %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "нужна утилита $1"; }

# ── Проверки окружения ────────────────────────────────────────

[ "$(id -u)" = "0" ] || die "нужны права root: запустите через sudo"

for tool in curl tar sed grep systemctl useradd install; do
    need "$tool"
done

case "$(uname -m)" in
    x86_64|amd64)  ARCH="x86_64" ;;
    aarch64|arm64) ARCH="aarch64" ;;
    *) die "неизвестная архитектура: $(uname -m)" ;;
esac

# Сборка под musl отличается от glibc, и перепутать их — значит получить
# «No such file or directory» на исправном бинарнике.
if [ -f /etc/alpine-release ] || ! ldd --version 2>&1 | grep -qi glibc; then
    LIBC="musl"
else
    LIBC="gnu"
fi

# ── Удаление ──────────────────────────────────────────────────

uninstall() {
    systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/${SERVICE}" "$SUDOERS"
    systemctl daemon-reload >/dev/null 2>&1 || true
    rm -rf "$DIR"
    userdel "$USER_NAME" >/dev/null 2>&1 || true
    ok "Бот-сторож удалён"
    exit 0
}

TOKEN="${TOKEN:-}"
CHAT="${CHAT:-}"
while [ $# -gt 0 ]; do
    case "$1" in
        --uninstall|--rollback) uninstall ;;
        --token) TOKEN="$2"; shift 2 ;;
        --chat)  CHAT="$2";  shift 2 ;;
        --repo)  REPO="$2";  shift 2 ;;
        *) shift ;;
    esac
done

# ── MTProxyL на месте и умеет проверять доступность ───────────

[ -x "$SCRIPT" ] || die "MTProxyL не найден: $SCRIPT"

# Вердикт доступности появился в 1.4.8. Проверяем не версию, а саму команду:
# версия — это то, что написано, а команда — то, что работает.
if ! "$SCRIPT" availability status --json >/dev/null 2>&1; then
    die "этот MTProxyL не умеет проверять доступность — нужна версия 1.4.8 или новее (mtproxyl update)"
fi
ok "MTProxyL на месте, вердикт доступности читается"

# ── Токен и чат ───────────────────────────────────────────────

if [ -z "$TOKEN" ] && [ -s "$CONFIG" ]; then
    # Переустановка поверх настроенного: прежние токен и чат подходят.
    TOKEN=$(sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
    [ -n "$CHAT" ] || CHAT=$(sed -n 's/.*"chat_id"[[:space:]]*:[[:space:]]*\(-\{0,1\}[0-9]*\).*/\1/p' "$CONFIG" | head -1)
fi

# Спрашиваем у терминала, а не со стандартного ввода: при `curl … | sh` ввод
# занят самим скриптом, который течёт по трубе, и read прочитал бы остаток его
# собственного текста вместо ответа человека. /dev/tty на месте и в этом
# случае — им и пользуемся.
ask() {
    ANSWER=""
    printf '  %s' "$1"
    if [ -t 0 ]; then
        read -r ANSWER && return 0
    elif read -r ANSWER < /dev/tty 2>/dev/null; then
        return 0
    fi
    # Терминала нет вовсе — запуск из cron, из панели, из чужого скрипта.
    # Проверяем попыткой открыть, а не через `[ -r /dev/tty ]`: файл может
    # существовать и при отвязанном терминале, и тогда чтение падает уже после
    # приглашения, оставляя человека гадать.
    printf '\n'
    die "спросить не у кого: запустите с --token T --chat ID"
}

if [ -z "$TOKEN" ]; then
    say "Токен даёт @BotFather: /newbot, затем /token."
    ask "Токен бота: "
    TOKEN="$ANSWER"
fi
if [ -z "$CHAT" ]; then
    say "ID чата: свой узнаете у @userinfobot."
    say "Можно указать группу или канал — тогда бот пишет туда."
    ask "ID чата: "
    CHAT="$ANSWER"
fi

[ -n "$TOKEN" ] || die "токен не задан"
case "$CHAT" in
    ''|*[!0-9-]*) die "ID чата — целое число" ;;
esac

# ── Данные о движке ───────────────────────────────────────────

API_PORT=9091
if [ -f "$SETTINGS" ]; then
    _p=$(sed -n "s/^PROXY_API_PORT=['\"]\{0,1\}\([0-9]*\).*/\1/p" "$SETTINGS" | head -1)
    [ -n "$_p" ] && API_PORT="$_p"
fi

# Заголовок авторизации читаем прямо из конфига движка — тем же способом, что
# и установщик панели: в settings.conf его нет намеренно, тот файл читается
# всем миром.
ENGINE_CONFIG=""
for _c in /etc/telemt/config.toml "${MTPROXYL_DIR}/config.toml" /opt/telemt/config.toml; do
    [ -f "$_c" ] && { ENGINE_CONFIG="$_c"; break; }
done
API_AUTH=""
if [ -n "$ENGINE_CONFIG" ]; then
    API_AUTH=$(sed -n "/^[[:space:]]*\[server\.api\]/,/^[[:space:]]*\[/p" "$ENGINE_CONFIG" 2>/dev/null \
        | sed -n "s/^[[:space:]]*auth_header[[:space:]]*=[[:space:]]*['\"]\(.*\)['\"].*/\1/p" | head -1)
fi
[ -n "$API_AUTH" ] || warn "заголовок авторизации API движка не найден — если движок его требует, впишите вручную в ${CONFIG}"

# ── Бинарник ──────────────────────────────────────────────────

TAG="${TAG:-}"
if [ -z "$TAG" ]; then
    TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
fi
[ -n "$TAG" ] || die "не удалось узнать последний релиз ${REPO}"

TAR="mtproxyl-alertbot-${ARCH}-linux-${LIBC}.tar.gz"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

say "Скачиваю ${TAR} (${TAG})"
curl -fsSL "https://github.com/${REPO}/releases/download/${TAG}/${TAR}" -o "${TMP}/${TAR}" \
    || die "не удалось скачать бинарник"

# Сверка контрольной суммы: оборванная закачка иначе молча превратилась бы в
# неработающую службу.
if command -v sha256sum >/dev/null 2>&1; then
    if curl -fsSL "https://github.com/${REPO}/releases/download/${TAG}/${TAR}.sha256" -o "${TMP}/sum" 2>/dev/null; then
        _want=$(awk '{print $1}' "${TMP}/sum")
        _have=$(sha256sum "${TMP}/${TAR}" | awk '{print $1}')
        [ "$_want" = "$_have" ] || die "контрольная сумма не сошлась — файл повреждён или подменён"
        ok "Контрольная сумма сошлась"
    else
        warn "контрольной суммы в релизе нет — проверка пропущена"
    fi
fi

tar -xzf "${TMP}/${TAR}" -C "$TMP" || die "архив не распаковался"
FOUND=$(find "$TMP" -type f -name 'mtproxyl-alertbot*' ! -name '*.tar.gz' | head -1)
[ -n "$FOUND" ] || die "в архиве нет бинарника"

# ── Пользователь, каталог, файлы ──────────────────────────────

if ! id "$USER_NAME" >/dev/null 2>&1; then
    useradd --system --no-create-home --home-dir "$DIR" --shell /usr/sbin/nologin "$USER_NAME" 2>/dev/null \
        || useradd --system --no-create-home --home-dir "$DIR" --shell /sbin/nologin "$USER_NAME" 2>/dev/null \
        || die "не удалось завести пользователя ${USER_NAME}"
fi

mkdir -p "$DIR"
install -m 0755 "$FOUND" "$BIN"

# Права урезаны до трёх подкоманд чтения: сторож не запускает прокси и не
# трогает пользователей, поэтому и разрешений на это не просит.
cat > "${TMP}/sudoers" << EOF
# MTProxyL — бот-сторож. Только чтение вердикта и проверка по кнопке.
${USER_NAME} ALL=(root) NOPASSWD: ${SCRIPT} availability status --json
${USER_NAME} ALL=(root) NOPASSWD: ${SCRIPT} availability details
${USER_NAME} ALL=(root) NOPASSWD: ${SCRIPT} availability check --json
Defaults:${USER_NAME} env_keep += "MTPROXYL_ASSUME_YES"
EOF
if command -v visudo >/dev/null 2>&1; then
    visudo -cf "${TMP}/sudoers" >/dev/null 2>&1 || die "правила sudo не прошли проверку"
fi
install -m 0440 "${TMP}/sudoers" "$SUDOERS"

# Конфиг пишем только при первой установке или при смене токена: там же лежит
# id живого сообщения, и затирать его на каждом обновлении значило бы плодить
# в чате новые сообщения.
if [ -s "$CONFIG" ]; then
    ok "Настройки на месте — не трогаю"
else
    cat > "${TMP}/config.json" << EOF
{
  "token": "${TOKEN}",
  "chat_id": ${CHAT},
  "alert_threshold": 60,
  "connect_fail_threshold": 20,
  "timezone": "",
  "script": "${SCRIPT}",
  "telemt_url": "http://127.0.0.1:${API_PORT}",
  "telemt_auth_header": "${API_AUTH}"
}
EOF
    install -m 0600 "${TMP}/config.json" "$CONFIG"
fi

chown -R "$USER_NAME":"$USER_NAME" "$DIR" 2>/dev/null || true
chmod 750 "$DIR"

cat > "/etc/systemd/system/${SERVICE}" << EOF
[Unit]
Description=MTProxyL alert bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
WorkingDirectory=${DIR}
ExecStart=${BIN} --config ${CONFIG}
Restart=on-failure
RestartSec=10

# Наблюдение — фон: на VPS с одним-двумя ядрами оно не должно отбирать
# процессор у прокси, ради которого всё и затевалось.
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
TimeoutStopSec=20

# NoNewPrivileges не ставим: он урезал бы sudo, которым бот спрашивает вердикт.
PrivateTmp=true
ProtectHome=true
ReadWritePaths=${DIR}

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now "$SERVICE" >/dev/null 2>&1 || true
sleep 1

if systemctl is-active "$SERVICE" >/dev/null 2>&1; then
    ok "Бот-сторож работает"
else
    warn "служба не поднялась — посмотрите: journalctl -u ${SERVICE} -n 50"
fi

say ""
say "Настройки:  ${BIN} config show"
say "Изменить:   ${BIN} config set alert_threshold 70"
say "Журнал:     journalctl -u ${SERVICE} -f"
say "Удалить:    запустите этот скрипт с --uninstall"
