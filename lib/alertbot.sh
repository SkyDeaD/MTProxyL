#!/bin/bash
# MTProxyL — алерт-бот: установка, настройка, служба.
#
# Второй вариант телеграм-бота, рядом с ботом-администратором. Управлять прокси
# он не умеет и намеренно не просит на это прав: его дело — одно живое
# сообщение в чате и звонок, когда прокси перестал работать. Смотрит он в две
# стороны: доступность из России (это вход, вердикт считает сам MTProxyL) и
# связь прокси с дата-центрами Telegram (это выход, данные берутся у движка).
#
# Устроен как бот-администратор: свой пользователь без прав, свой каталог,
# своя служба, поимённый sudoers. Отличие одно — это готовый бинарник из
# релиза, а не питон в venv, поэтому вместо pip здесь скачивание с проверкой
# контрольной суммы.

ALERTBOT_DIR="/opt/mtproxyl-alertbot"
ALERTBOT_USER="mtproxyl-alertbot"
ALERTBOT_SERVICE="mtproxyl-alertbot.service"
ALERTBOT_SUDOERS="/etc/sudoers.d/mtproxyl-alertbot"
ALERTBOT_CONFIG="${ALERTBOT_DIR}/config.json"
ALERTBOT_BIN="${ALERTBOT_DIR}/mtproxyl-alertbot"
ALERTBOT_REPO="${ALERTBOT_REPO:-SkyDeaD/MTProxyL}"

alertbot_installed() {
    [ -x "$ALERTBOT_BIN" ]
}

alertbot_service_active() {
    systemctl is-active "$ALERTBOT_SERVICE" &>/dev/null
}

alertbot_configured() {
    [ -s "$ALERTBOT_CONFIG" ] && \
        grep -q '"token"[[:space:]]*:[[:space:]]*"[^"]\+"' "$ALERTBOT_CONFIG" 2>/dev/null
}

alertbot_status_line() {
    if ! alertbot_installed; then
        echo "не установлен"
    elif ! alertbot_configured; then
        echo "установлен, не настроен"
    elif alertbot_service_active; then
        echo "работает"
    else
        echo "остановлен"
    fi
}

# ── Установка ─────────────────────────────────────────────────

_alertbot_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "x86_64" ;;
        aarch64|arm64) echo "aarch64" ;;
        *) return 1 ;;
    esac
}

# Сборка под musl отличается от glibc, и перепутать их — значит получить
# «No such file or directory» на исправном бинарнике.
_alertbot_libc() {
    if [ -f /etc/alpine-release ] || ! ldd --version 2>&1 | grep -qi glibc; then
        echo "musl"
    else
        echo "gnu"
    fi
}

_alertbot_ensure_user() {
    id "$ALERTBOT_USER" &>/dev/null && return 0
    useradd --system --no-create-home --home-dir "$ALERTBOT_DIR" \
            --shell /usr/sbin/nologin "$ALERTBOT_USER" 2>/dev/null || \
    useradd --system --no-create-home --home-dir "$ALERTBOT_DIR" \
            --shell /sbin/nologin "$ALERTBOT_USER" 2>/dev/null || {
        log_error "Не удалось завести пользователя ${ALERTBOT_USER}"
        return 1
    }
    return 0
}

# Права урезаны до трёх подкоманд чтения. Алерт-бот не запускает прокси, не
# трогает пользователей и не делает бэкапов — и разрешений на это не просит:
# процесс с сетевым вводом должен уметь ровно то, что ему нужно.
_alertbot_write_sudoers() {
    local _script="${INSTALL_DIR}/mtproxyl.sh" _tmp
    _tmp=$(mktemp) || return 1
    cat > "$_tmp" << EOF
# MTProxyL — алерт-бот. Только чтение вердикта и проверка по кнопке.
${ALERTBOT_USER} ALL=(root) NOPASSWD: ${_script} availability status --json
${ALERTBOT_USER} ALL=(root) NOPASSWD: ${_script} availability details
${ALERTBOT_USER} ALL=(root) NOPASSWD: ${_script} availability check --json
Defaults:${ALERTBOT_USER} env_keep += "MTPROXYL_ASSUME_YES"
EOF
    if ! visudo -cf "$_tmp" >/dev/null 2>&1; then
        rm -f "$_tmp"
        log_error "Правила sudo не прошли проверку — бот остался бы без доступа к вердикту"
        return 1
    fi
    install -m 0440 "$_tmp" "$ALERTBOT_SUDOERS"
    rm -f "$_tmp"
}

_alertbot_write_service() {
    cat > "/etc/systemd/system/${ALERTBOT_SERVICE}" << EOF
[Unit]
Description=MTProxyL alert bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${ALERTBOT_USER}
WorkingDirectory=${ALERTBOT_DIR}
ExecStart=${ALERTBOT_BIN} --config ${ALERTBOT_CONFIG}
Restart=on-failure
RestartSec=10

# Наблюдение — фон. На VPS с одним-двумя ядрами оно не должно отбирать
# процессор у прокси, ради которого всё и затевалось.
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
# Двадцати секунд хватает, чтобы дописать начатую правку сообщения; полторы
# минуты по умолчанию выглядели бы зависшим перезапуском из панели.
TimeoutStopSec=20

# NoNewPrivileges не ставим: он урезал бы sudo, которым бот спрашивает вердикт.
PrivateTmp=true
ProtectHome=true
ReadWritePaths=${ALERTBOT_DIR}

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload 2>/dev/null
}

# Бинарник качается из релиза форка и сверяется по sha256. Без сверки
# оборванная закачка молча превратилась бы в неработающую службу.
_alertbot_fetch_binary() {
    local _tag="$1" _arch _libc _tar _url _tmpdir _sum _actual
    _arch=$(_alertbot_arch) || { log_error "Неизвестная архитектура: $(uname -m)"; return 1; }
    _libc=$(_alertbot_libc)
    _tar="mtproxyl-alertbot-${_arch}-linux-${_libc}.tar.gz"
    _url="https://github.com/${ALERTBOT_REPO}/releases/download/${_tag}/${_tar}"

    _tmpdir=$(mktemp -d) || return 1
    log_info "Скачиваю ${_tar}"
    if ! curl -fsSL "$_url" -o "${_tmpdir}/${_tar}"; then
        rm -rf "$_tmpdir"
        log_error "Не удалось скачать бинарник алерт-бота"
        return 1
    fi

    # Сумму берём из общего checksums.txt: его имя в релизе постоянно, а имя
    # отдельного файла зависит от того, как назван архив, — на этом мы уже
    # однажды промахнулись, и проверка молча не выполнялась.
    if command -v sha256sum >/dev/null 2>&1; then
        if curl -fsSL "https://github.com/${ALERTBOT_REPO}/releases/download/${_tag}/checksums.txt" \
                -o "${_tmpdir}/sums" 2>/dev/null; then
            _sum=$(grep " ${_tar}\$" "${_tmpdir}/sums" | awk '{print $1}' | head -1)
            _actual=$(sha256sum "${_tmpdir}/${_tar}" | awk '{print $1}')
            if [ -z "$_sum" ]; then
                log_warn "В checksums.txt нет строки про ${_tar} — проверка пропущена"
            elif [ "$_sum" != "$_actual" ]; then
                rm -rf "$_tmpdir"
                log_error "Контрольная сумма не сошлась — файл повреждён или подменён"
                return 1
            fi
        else
            log_warn "Контрольных сумм в релизе нет — проверка пропущена"
        fi
    fi

    if ! tar -xzf "${_tmpdir}/${_tar}" -C "$_tmpdir" 2>/dev/null; then
        rm -rf "$_tmpdir"
        log_error "Архив не распаковался"
        return 1
    fi

    local _found
    _found=$(find "$_tmpdir" -type f -name 'mtproxyl-alertbot' | head -1)
    if [ -z "$_found" ]; then
        rm -rf "$_tmpdir"
        log_error "В архиве нет бинарника mtproxyl-alertbot"
        return 1
    fi

    install -m 0755 "$_found" "$ALERTBOT_BIN"
    rm -rf "$_tmpdir"
    return 0
}

# Копия установщика рядом с ботом. Ею панель нажимает «Установить»,
# «Обновить» и «Удалить»: своего способа поставить бота у неё нет, а
# установка из меню оставляла её без этого файла — и кнопки упирались в
# «No such file».
_alertbot_fetch_installer() {
    local _branch="${ALERTBOT_BRANCH:-tg-testing}" _tmp
    _tmp=$(mktemp) || return 0
    if curl -fsSL "https://raw.githubusercontent.com/${ALERTBOT_REPO}/${_branch}/install-alertbot.sh" \
            -o "$_tmp" 2>/dev/null && sh -n "$_tmp" 2>/dev/null; then
        install -m 0755 "$_tmp" "${ALERTBOT_DIR}/install-alertbot.sh"
    else
        log_warn "Копию установщика скачать не удалось — управление ботом из панели будет недоступно"
    fi
    rm -f "$_tmp"
    return 0
}

# Последний релиз форка. Отдельной функцией: при обновлении бота тег снова
# нужен, а зашивать его в код значило бы обновляться правкой скрипта.
_alertbot_latest_tag() {
    curl -fsSL "https://api.github.com/repos/${ALERTBOT_REPO}/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

# ── Настройки движка ──────────────────────────────────────────

# Адрес и заголовок API движка — то же, что выясняет установщик панели: порт из
# настроек, заголовок из конфига самого движка. В настройках его нет намеренно,
# settings.conf читается всем миром.
_alertbot_telemt_url() {
    local _port="${PROXY_API_PORT:-9091}"
    echo "http://127.0.0.1:${_port}"
}

_alertbot_telemt_auth() {
    local _cfg
    _cfg=$(_engine_config_path 2>/dev/null) || _cfg=""
    [ -n "$_cfg" ] && [ -f "$_cfg" ] || return 0
    sed -n "/^[[:space:]]*\[server\.api\]/,/^[[:space:]]*\[/p" "$_cfg" 2>/dev/null \
        | sed -n "s/^[[:space:]]*auth_header[[:space:]]*=[[:space:]]*['\"]\(.*\)['\"].*/\1/p" \
        | head -1
}

_alertbot_write_config() {
    local _token="$1" _chat="$2" _tmp _url _auth
    _url=$(_alertbot_telemt_url)
    _auth=$(_alertbot_telemt_auth)

    _tmp=$(mktemp "${ALERTBOT_DIR}/.config.XXXXXX") || return 1
    cat > "$_tmp" << EOF
{
  "token": "${_token}",
  "chat_id": ${_chat},
  "alert_threshold": 60,
  "connect_fail_threshold": 20,
  "timezone": "",
  "script": "${INSTALL_DIR}/mtproxyl.sh",
  "telemt_url": "${_url}",
  "telemt_auth_header": "${_auth}"
}
EOF
    mv -f "$_tmp" "$ALERTBOT_CONFIG"
    chown "$ALERTBOT_USER":"$ALERTBOT_USER" "$ALERTBOT_CONFIG" 2>/dev/null || true
    chmod 600 "$ALERTBOT_CONFIG"
}

# Настройки правятся по одной, как у бота-администратора: панель и меню шлют
# сюда ключ и значение, а не переписывают файл целиком.
_alertbot_cfg_set() {
    local _key="$1" _val="$2" _tmp
    [ -s "$ALERTBOT_CONFIG" ] || { log_error "Бот не настроен"; return 1; }
    _tmp=$(mktemp "${ALERTBOT_DIR}/.config.XXXXXX") || return 1
    if ! jq "$_key = $_val" "$ALERTBOT_CONFIG" > "$_tmp" 2>/dev/null; then
        rm -f "$_tmp"
        log_error "Не удалось записать настройку"
        return 1
    fi
    mv -f "$_tmp" "$ALERTBOT_CONFIG"
    chown "$ALERTBOT_USER":"$ALERTBOT_USER" "$ALERTBOT_CONFIG" 2>/dev/null || true
    chmod 600 "$ALERTBOT_CONFIG"
}

alertbot_set_param() {
    local _key="$1" _val="$2"
    case "$_key" in
        alert_threshold|connect_fail_threshold)
            [[ "$_val" =~ ^[0-9]+$ ]] && [ "$_val" -ge 1 ] && [ "$_val" -le 99 ] || {
                log_error "Порог задаётся числом от 1 до 99"
                return 1
            }
            _alertbot_cfg_set ".${_key}" "$_val" || return 1
            ;;
        timezone)
            # Пустое значение — «определять самому»: это законный выбор.
            if [ -n "$_val" ] && [ ! -f "/usr/share/zoneinfo/${_val}" ]; then
                log_error "Неизвестный часовой пояс: ${_val}"
                return 1
            fi
            _alertbot_cfg_set ".timezone" "\"$_val\"" || return 1
            ;;
        chat_id)
            [[ "$_val" =~ ^-?[0-9]+$ ]] || { log_error "ID чата — целое число"; return 1; }
            _alertbot_cfg_set ".chat_id" "$_val" || return 1
            ;;
        *)
            log_error "Неизвестная настройка: ${_key}"
            return 1
            ;;
    esac
    systemctl restart "$ALERTBOT_SERVICE" 2>/dev/null || true
    return 0
}

# ── Установка и снятие ────────────────────────────────────────

alertbot_install() {
    local _token="" _chat="" _tag=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --token) _token="$2"; shift 2 ;;
            --chat|--admin) _chat="$2"; shift 2 ;;
            --tag) _tag="$2"; shift 2 ;;
            *) shift ;;
        esac
    done

    command -v jq >/dev/null 2>&1 || { log_error "Нужен jq"; return 1; }
    command -v curl >/dev/null 2>&1 || { log_error "Нужен curl"; return 1; }

    if [ -z "$_token" ] || [ -z "$_chat" ]; then
        if alertbot_configured; then
            # Переустановка поверх настроенного: токен и чат уже есть.
            _token=$(jq -r '.token // ""' "$ALERTBOT_CONFIG" 2>/dev/null)
            _chat=$(jq -r '.chat_id // 0' "$ALERTBOT_CONFIG" 2>/dev/null)
        else
            log_error "Нужны токен и ID чата: alertbot install --token T --chat ID"
            return 1
        fi
    fi

    [[ "$_token" =~ ^[0-9]{5,}:[A-Za-z0-9_-]{30,}$ ]] || {
        log_error "Токен не похож на выданный @BotFather"
        return 1
    }
    [[ "$_chat" =~ ^-?[0-9]+$ ]] || { log_error "ID чата — целое число"; return 1; }

    [ -z "$_tag" ] && _tag=$(_alertbot_latest_tag)
    [ -n "$_tag" ] || { log_error "Не удалось узнать последний релиз"; return 1; }

    _alertbot_ensure_user || return 1
    mkdir -p "$ALERTBOT_DIR"
    chown "$ALERTBOT_USER":"$ALERTBOT_USER" "$ALERTBOT_DIR" 2>/dev/null || true
    chmod 750 "$ALERTBOT_DIR"

    _alertbot_fetch_binary "$_tag" || return 1
    _alertbot_fetch_installer
    _alertbot_write_sudoers || return 1
    _alertbot_write_config "$_token" "$_chat" || return 1
    _alertbot_write_service

    # Второй бот в чате — это два опросчика на один и тот же токен и путаница в
    # том, кто чем управляет. Поэтому вариант ровно один.
    alertbot_take_over

    # enable и restart порознь: «--now» на уже активной службе делает start,
    # а он для неё пустышка — подменённый бинарник так и остался бы на диске,
    # пока в памяти крутится прежний процесс.
    systemctl enable "$ALERTBOT_SERVICE" >/dev/null 2>&1
    systemctl restart "$ALERTBOT_SERVICE" >/dev/null 2>&1
    sleep 1
    if alertbot_service_active; then
        log_success "Алерт-бот установлен и работает"
    else
        log_warn "Служба не поднялась — посмотрите: journalctl -u ${ALERTBOT_SERVICE} -n 50"
    fi
    return 0
}

# bot_use_alert делает сторожа активным: включает его и останавливает чужого.
#
# Именно enable, а не только start: без него после перезагрузки сервера
# поднимется прежний бот, и человек узнает об этом, когда тревоги не придут.
# Чужого останавливаем, но не сносим — там настройки и права, заведённые
# руками.
bot_use_alert() {
    alertbot_installed || { log_error "Бот-сторож не установлен"; return 1; }

    if declare -f tgbot_installed >/dev/null 2>&1 && tgbot_installed; then
        log_info "Останавливаю бота-администратора: включённым может быть только один"
        systemctl disable --now "$TGBOT_SERVICE" >/dev/null 2>&1 || true
    fi

    systemctl enable "$ALERTBOT_SERVICE" >/dev/null 2>&1 || true
    systemctl restart "$ALERTBOT_SERVICE" >/dev/null 2>&1 || true

    TGBOT_VARIANT="alert"
    save_settings 2>/dev/null || true

    if alertbot_service_active; then
        log_success "Активен бот-сторож"
    else
        log_warn "Сторож не поднялся — journalctl -u ${ALERTBOT_SERVICE} -n 50"
        return 1
    fi
}

# take_over — то же самое, но зовётся из установки, когда служба поднимается
# сразу после и перезапускать её отдельно незачем.
alertbot_take_over() {
    if declare -f tgbot_installed >/dev/null 2>&1 && tgbot_installed; then
        if systemctl is-active "$TGBOT_SERVICE" &>/dev/null; then
            log_info "Останавливаю бота-администратора: включённым может быть только один"
            systemctl disable --now "$TGBOT_SERVICE" >/dev/null 2>&1
        fi
    fi
    TGBOT_VARIANT="alert"
    save_settings 2>/dev/null || true
}

alertbot_update() {
    alertbot_installed || { log_error "Алерт-бот не установлен"; return 1; }
    local _tag
    _tag=$(_alertbot_latest_tag)
    [ -n "$_tag" ] || { log_error "Не удалось узнать последний релиз"; return 1; }
    _alertbot_fetch_binary "$_tag" || return 1
    _alertbot_fetch_installer
    # Список разрешённых команд переписываем при каждом обновлении: новая
    # версия однажды упрётся в отказ на команде, которой в старых правилах нет.
    _alertbot_write_sudoers || return 1
    systemctl restart "$ALERTBOT_SERVICE" 2>/dev/null || true
    log_success "Алерт-бот обновлён до ${_tag}"
}

alertbot_uninstall() {
    local _yes="$1"
    if [ "$_yes" != "--yes" ] && [ "${MTPROXYL_ASSUME_YES:-}" != "1" ]; then
        read -rp "Удалить алерт-бота? [y/N]: " _ans
        [[ "$_ans" =~ ^[Yy]$ ]] || return 0
    fi
    systemctl disable --now "$ALERTBOT_SERVICE" >/dev/null 2>&1
    rm -f "/etc/systemd/system/${ALERTBOT_SERVICE}" "$ALERTBOT_SUDOERS"
    systemctl daemon-reload 2>/dev/null
    rm -rf "$ALERTBOT_DIR"
    userdel "$ALERTBOT_USER" 2>/dev/null || true
    if [ "${TGBOT_VARIANT:-}" = "alert" ]; then
        TGBOT_VARIANT="none"
        save_settings 2>/dev/null || true
    fi
    log_success "Алерт-бот удалён"
}

# ── Наружу ────────────────────────────────────────────────────

alertbot_status_json() {
    local _installed=false _configured=false _active=false _enabled=false
    alertbot_installed && _installed=true
    alertbot_configured && _configured=true
    alertbot_service_active && _active=true
    systemctl is-enabled "$ALERTBOT_SERVICE" &>/dev/null && _enabled=true

    local _cfg='{}'
    if [ -s "$ALERTBOT_CONFIG" ]; then
        # Токен наружу не отдаём никогда — только признак, что он задан.
        _cfg=$(jq -c '{chat_id, alert_threshold, connect_fail_threshold, timezone,
                       has_token: ((.token // "") != "")}' "$ALERTBOT_CONFIG" 2>/dev/null) || _cfg='{}'
    fi

    jq -nc --argjson installed "$_installed" --argjson configured "$_configured" \
           --argjson active "$_active" --argjson enabled "$_enabled" \
           --arg dir "$ALERTBOT_DIR" --arg service "$ALERTBOT_SERVICE" \
           --argjson config "$_cfg" \
        '{installed:$installed, configured:$configured, active:$active,
          enabled:$enabled, dir:$dir, service:$service, config:$config}'
}

alertbot_logs_json() {
    local _lines="${1:-100}"
    [[ "$_lines" =~ ^[0-9]+$ ]] || _lines=100
    [ "$_lines" -gt 500 ] && _lines=500
    local _out
    _out=$(journalctl -u "$ALERTBOT_SERVICE" -n "$_lines" --no-pager 2>/dev/null)
    jq -nc --arg lines "$_out" '{lines:$lines}'
}

alertbot_show_status() {
    echo
    echo "  Алерт-бот: $(alertbot_status_line)"
    alertbot_installed || return 0
    echo "  Каталог:   ${ALERTBOT_DIR}"
    echo "  Служба:    ${ALERTBOT_SERVICE}"
    if [ -s "$ALERTBOT_CONFIG" ]; then
        echo "  Чат:       $(jq -r '.chat_id // "не задан"' "$ALERTBOT_CONFIG" 2>/dev/null)"
        echo "  Пороги:    доступность $(jq -r '.alert_threshold // 60' "$ALERTBOT_CONFIG" 2>/dev/null)%, отказы $(jq -r '.connect_fail_threshold // 20' "$ALERTBOT_CONFIG" 2>/dev/null)%"
    fi
    echo
}

handle_alertbot_command() {
    local _cmd="${1:-status}"
    shift 2>/dev/null || true
    check_root
    case "$_cmd" in
        install)   alertbot_install "$@" ;;
        update)    alertbot_update ;;
        uninstall) alertbot_uninstall "$@" ;;
        set)       alertbot_set_param "$@" ;;
        use|activate) bot_use_alert ;;
        start)     systemctl start "$ALERTBOT_SERVICE" && log_success "Запущен" ;;
        stop)      systemctl stop "$ALERTBOT_SERVICE" && log_success "Остановлен" ;;
        restart)   systemctl restart "$ALERTBOT_SERVICE" && log_success "Перезапущен" ;;
        logs)
            if [ "${1:-}" = "--json" ]; then
                alertbot_logs_json "${2:-100}"
            else
                journalctl -u "$ALERTBOT_SERVICE" -n "${1:-60}" --no-pager
            fi
            ;;
        status)
            if [ "${1:-}" = "--json" ]; then
                alertbot_status_json
            else
                alertbot_show_status
            fi
            ;;
        *)
            echo "Неизвестная команда: alertbot ${_cmd}"
            return 1
            ;;
    esac
}
