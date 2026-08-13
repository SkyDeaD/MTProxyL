#!/bin/bash
# MTProxyL — подменю: алерт-бот (сторож).

# Установка сторожа из меню: спрашиваем то же, что спрашивает бот-администратор.
tui_alertbot_install() {
    echo ""
    echo -e "  ${DIM}Токен даёт @BotFather: /newbot, затем /token.${NC}"
    echo -en "  ${BOLD}Токен бота:${NC} "
    local _token; read_line _token
    [ -n "$_token" ] || { log_info "Отменено"; return 0; }

    echo ""
    echo -e "  ${DIM}ID чата: свой узнаете у @userinfobot. Можно указать группу или${NC}"
    echo -e "  ${DIM}канал — тогда бот пишет туда, а команды и кнопки там не нужны.${NC}"
    echo -en "  ${BOLD}ID чата:${NC} "
    local _chat; read_line _chat
    [ -n "$_chat" ] || { log_info "Отменено"; return 0; }

    alertbot_install --token "$_token" --chat "$_chat"
}

tui_alertbot_menu() {
    while true; do
        clear_screen
        draw_header "БОТ-СТОРОЖ"
        echo ""

        if ! alertbot_installed; then
            echo -e "  ${BOLD}Состояние:${NC} ${DIM}не установлен${NC}"
            echo ""
            echo -e "  ${DIM}Сторож не управляет прокси и не просит на это прав. Он держит${NC}"
            echo -e "  ${DIM}в чате одно живое сообщение и будит звуком, когда прокси${NC}"
            echo -e "  ${DIM}перестал работать — снаружи или изнутри.${NC}"
            echo ""
            echo -e "  ${GREEN}[1]${NC}  Установить сторожа"
            echo ""
            echo -e "  ${DIM}[0]${NC}  Назад"
            echo ""
            local c; c=$(read_choice "выбор" "0")
            case "$c" in
                1) tui_alertbot_install; press_any_key ;;
                0|"") return ;;
            esac
            continue
        fi

        echo -e "  ${BOLD}Состояние:${NC}   $(alertbot_status_line)"
        echo -e "  ${BOLD}Каталог:${NC}     ${ALERTBOT_DIR}"
        if [ -s "$ALERTBOT_CONFIG" ]; then
            echo -e "  ${BOLD}Чат:${NC}         $(jq -r '.chat_id // "не задан"' "$ALERTBOT_CONFIG" 2>/dev/null)"
            echo -e "  ${BOLD}Пороги:${NC}      доступность $(jq -r '.alert_threshold // 60' "$ALERTBOT_CONFIG" 2>/dev/null)%, отказы к дата-центрам $(jq -r '.connect_fail_threshold // 20' "$ALERTBOT_CONFIG" 2>/dev/null)%"
            local _tz; _tz=$(jq -r '.timezone // ""' "$ALERTBOT_CONFIG" 2>/dev/null)
            echo -e "  ${BOLD}Часовой пояс:${NC} ${_tz:-${DIM}определяется сам${NC}}"
        fi
        echo ""

        echo -e "  ${CYAN}[1]${NC}  $(alertbot_service_active && echo "Перезапустить" || echo "Запустить") сторожа"
        echo -e "  ${CYAN}[2]${NC}  Остановить"
        echo -e "  ${CYAN}[3]${NC}  Токен и чат заново"
        echo -e "  ${CYAN}[4]${NC}  Порог доступности"
        echo -e "  ${CYAN}[5]${NC}  Порог отказов к дата-центрам"
        echo -e "  ${CYAN}[6]${NC}  Часовой пояс"
        echo -e "  ${CYAN}[7]${NC}  Журнал службы"
        echo -e "  ${CYAN}[8]${NC}  Обновить бота"
        echo -e "  ${RED}[9]${NC}  Удалить сторожа"
        echo ""
        echo -e "  ${DIM}[0]${NC}  Назад"
        echo ""

        local c; c=$(read_choice "выбор" "0")
        case "$c" in
            1) systemctl restart "$ALERTBOT_SERVICE" && log_success "Готово"; press_any_key ;;
            2) systemctl stop "$ALERTBOT_SERVICE" && log_success "Остановлен"; press_any_key ;;
            3) tui_alertbot_install; press_any_key ;;
            4) _tui_alertbot_threshold alert_threshold "Порог доступности, %" ;;
            5) _tui_alertbot_threshold connect_fail_threshold "Порог отказов к дата-центрам, %" ;;
            6) _tui_alertbot_timezone ;;
            7) journalctl -u "$ALERTBOT_SERVICE" -n 60 --no-pager; press_any_key ;;
            8) alertbot_update; press_any_key ;;
            9) alertbot_uninstall; press_any_key; return ;;
            0|"") return ;;
        esac
    done
}

_tui_alertbot_threshold() {
    local _key="$1" _title="$2"
    echo ""
    echo -en "  ${BOLD}${_title} (1-99):${NC} "
    local _val; read_line _val
    [ -n "$_val" ] || return 0
    alertbot_set_param "$_key" "$_val"
    press_any_key
}

_tui_alertbot_timezone() {
    echo ""
    echo -e "  ${DIM}Пусто — определять самому. Пример: Asia/Tashkent, Europe/Moscow.${NC}"
    echo -en "  ${BOLD}Часовой пояс:${NC} "
    local _val; read_line _val
    alertbot_set_param timezone "$_val"
    press_any_key
}

# Точка входа из главного меню. Всегда экран выбора, а не угадывание: ботов
# два, и человек, поставивший одного, должен видеть второго — иначе он про него
# просто не узнает.
tui_bot_menu() {
    while true; do
        clear_screen
        draw_header "ТЕЛЕГРАМ БОТ"
        echo ""

        local _active _admin_mark="" _alert_mark=""
        _active=$(_bot_active_variant)
        case "$_active" in
            admin) _admin_mark="  ${GREEN}◄ активен${NC}" ;;
            alert) _alert_mark="  ${GREEN}◄ активен${NC}" ;;
        esac

        echo -e "  ${DIM}Работать может только один. Сейчас активен:${NC} ${BOLD}$(_bot_variant_name "$_active")${NC}"
        echo ""
        echo -e "  ${CYAN}[1]${NC}  Бот-администратор   ${DIM}$(tgbot_status_line)${NC}${_admin_mark}"
        echo -e "       ${DIM}управление прокси кнопками в чате: пользователи, ссылки, трафик${NC}"
        echo ""
        echo -e "  ${CYAN}[2]${NC}  Бот-сторож          ${DIM}$(alertbot_status_line)${NC}${_alert_mark}"
        echo -e "       ${DIM}тревоги: доступность из РФ и связь прокси с дата-центрами${NC}"
        echo ""

        # Переключать есть на что только когда установлены оба: иначе пункт
        # обещал бы то, чего сделать нельзя.
        local _switch=""
        if tgbot_installed && alertbot_installed; then
            if [ "$_active" = "alert" ]; then
                _switch="admin"
                echo -e "  ${CYAN}[3]${NC}  Сделать активным бота-администратора"
            else
                _switch="alert"
                echo -e "  ${CYAN}[3]${NC}  Сделать активным бота-сторожа"
            fi
            echo ""
        fi

        echo -e "  ${DIM}[0]${NC}  Назад"
        echo ""

        local c; c=$(read_choice "выбор" "0")
        case "$c" in
            1) tui_tgbot_menu ;;
            2) tui_alertbot_menu ;;
            3)
                [ -n "$_switch" ] || continue
                if [ "$_switch" = "alert" ]; then
                    bot_use_alert
                else
                    bot_use_admin
                fi
                press_any_key ;;
            0|"") return ;;
        esac
    done
}

# _bot_active_variant отвечает, кто работает НА САМОМ ДЕЛЕ, а не что записано
# в настройках: службу могли остановить руками, и врать об этом нельзя.
_bot_active_variant() {
    if alertbot_installed && alertbot_service_active; then
        echo "alert"
    elif tgbot_installed && tgbot_service_active; then
        echo "admin"
    else
        echo "none"
    fi
}

_bot_variant_name() {
    case "$1" in
        admin) echo "бот-администратор" ;;
        alert) echo "бот-сторож" ;;
        *)     echo "никто" ;;
    esac
}

# Строка состояния для главного меню: называет вариант, а не просто «бот».
bot_status_line() {
    local _active
    _active=$(_bot_active_variant)
    case "$_active" in
        admin) echo "администратор: работает" ;;
        alert) echo "сторож: работает" ;;
    esac
    [ "$_active" = "none" ] || return 0

    if tgbot_installed && alertbot_installed; then
        echo "оба установлены, остановлены"
    elif tgbot_installed; then
        echo "администратор: $(tgbot_status_line)"
    elif alertbot_installed; then
        echo "сторож: $(alertbot_status_line)"
    else
        echo "не установлен"
    fi
}
