#!/bin/sh
# Доставка сборки MTProxyL-Panel с телеграм-ботом поверх уже работающей панели.
#
# Меняет ровно один файл — бинарник панели. Конфиг, данные (токены, цель
# проверки, сертификаты), движок telemt и пользователи не трогаются: панель
# собрана одним статическим файлом со встроенным фронтендом, поэтому обновление
# это замена файла и перезапуск.
#
#   curl -fsSL <URL>/install-tgbot.sh | sh
#   curl -fsSL <URL>/install-tgbot.sh | sh -s -- --rollback
#
# Переменные окружения на случай нестандартной установки:
#   REPO, TAG, PANEL_BINARY, SERVICE_NAME, DATA_DIR, CONTAINER
set -eu

REPO="${REPO:-SkyDeaD/MTProxyL}"
TAG="${TAG:-mtproxyl-panel-v1.0.7-tg4}"
PANEL_BINARY="${PANEL_BINARY:-/usr/local/bin/mtproxyl-panel}"
SERVICE_NAME="${SERVICE_NAME:-mtproxyl-panel}"
DATA_DIR="${DATA_DIR:-/var/lib/mtproxyl-panel}"
BACKUP="$DATA_DIR/staging/mtproxyl-panel.pre-tgbot"

say() { printf '%s\n' "$*" >&2; }
die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || die "нужны права root, а sudo не найден"
  SUDO="sudo"
fi

TEMP_DIR=""
cleanup() { [ -n "$TEMP_DIR" ] && rm -rf "$TEMP_DIR"; }
trap cleanup EXIT INT TERM

# ── Где стоит панель ─────────────────────────────────────────────────────────
# Docker и systemd обновляются по-разному, и перепутать их значит перезапустить
# не то. Systemd проверяется первым: контейнер telemt может быть на хосте, где
# сама панель поставлена бинарником.
detect_install() {
  if command -v systemctl >/dev/null 2>&1 && $SUDO systemctl cat "$SERVICE_NAME" >/dev/null 2>&1; then
    echo systemd
    return
  fi
  if command -v docker >/dev/null 2>&1; then
    CONTAINER="${CONTAINER:-$(docker ps --format '{{.Names}} {{.Image}}' 2>/dev/null \
      | grep -i mtproxyl-panel | head -1 | cut -d' ' -f1)}"
    if [ -n "${CONTAINER:-}" ]; then
      echo docker
      return
    fi
  fi
  echo none
}

arch_name() {
  case "$(uname -m)" in
    x86_64|amd64) echo x86_64 ;;
    aarch64|arm64) echo aarch64 ;;
    *) die "архитектура $(uname -m) не поддерживается: релизы собираются под x86_64 и aarch64" ;;
  esac
}

# ── Загрузка и проверка ──────────────────────────────────────────────────────
fetch_binary() {
  _arch="$(arch_name)"
  _archive="mtproxyl-panel-${_arch}-linux-gnu.tar.gz"
  _base="https://github.com/$REPO/releases/download/$TAG"

  TEMP_DIR="$(mktemp -d)"
  say "Скачиваю $TAG ($_arch)..."
  curl -fsSL "$_base/$_archive" -o "$TEMP_DIR/$_archive" \
    || die "не удалось скачать $_base/$_archive — проверьте, что релиз $TAG опубликован"
  curl -fsSL "$_base/mtproxyl-panel-${_arch}-linux-gnu.sha256" -o "$TEMP_DIR/sum" \
    || die "не удалось скачать контрольную сумму"

  # Ставить непроверенный бинарник, скачанный по сети, нельзя: он пойдёт под
  # тем же пользователем, что и панель, и с доступом к её данным.
  _want="$(cut -d' ' -f1 < "$TEMP_DIR/sum")"
  if command -v sha256sum >/dev/null 2>&1; then
    _got="$(sha256sum "$TEMP_DIR/$_archive" | cut -d' ' -f1)"
  elif command -v shasum >/dev/null 2>&1; then
    _got="$(shasum -a 256 "$TEMP_DIR/$_archive" | cut -d' ' -f1)"
  else
    die "нет ни sha256sum, ни shasum — проверить контрольную сумму нечем"
  fi
  [ "$_want" = "$_got" ] || die "контрольная сумма не сошлась: ожидалось $_want, получено $_got"
  say "Контрольная сумма сошлась."

  tar xzf "$TEMP_DIR/$_archive" -C "$TEMP_DIR" || die "архив не распаковался"
  NEW_BINARY="$TEMP_DIR/mtproxyl-panel-${_arch}-linux"
  [ -f "$NEW_BINARY" ] || die "в архиве нет файла mtproxyl-panel-${_arch}-linux"
  chmod +x "$NEW_BINARY"
}

backup_current() {
  _src="$1"
  $SUDO mkdir -p "$(dirname "$BACKUP")"
  if [ -f "$_src" ]; then
    $SUDO cp -f "$_src" "$BACKUP"
    say "Прежний бинарник сохранён: $BACKUP"
  fi
}

# ── systemd ──────────────────────────────────────────────────────────────────
install_systemd() {
  [ -f "$PANEL_BINARY" ] || die "не найден $PANEL_BINARY — панель установлена иначе, задайте PANEL_BINARY"
  backup_current "$PANEL_BINARY"

  $SUDO install -m 0755 "$NEW_BINARY" "$PANEL_BINARY" || die "не удалось заменить $PANEL_BINARY"
  say "Перезапускаю $SERVICE_NAME..."
  $SUDO systemctl restart "$SERVICE_NAME" || { restore_systemd; die "служба не перезапустилась"; }

  # Служба может подняться и тут же упасть на разборе конфига — проверяем не
  # факт запуска, а то, что через несколько секунд она ещё жива.
  sleep 3
  if ! $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
    restore_systemd
    say "Журнал последних строк:"
    $SUDO journalctl -u "$SERVICE_NAME" -n 20 --no-pager >&2 || true
    die "панель не поднялась — вернул прежний бинарник"
  fi
  say "Готово. Версия: $($SUDO "$PANEL_BINARY" version 2>/dev/null || echo '?')"
}

restore_systemd() {
  [ -f "$BACKUP" ] || return 0
  say "Возвращаю прежний бинарник..."
  $SUDO install -m 0755 "$BACKUP" "$PANEL_BINARY"
  $SUDO systemctl restart "$SERVICE_NAME" || true
}

# ── docker ───────────────────────────────────────────────────────────────────
install_docker() {
  say "Панель найдена в контейнере: $CONTAINER"
  _inner="$(docker inspect -f '{{range .Config.Entrypoint}}{{.}} {{end}}' "$CONTAINER" 2>/dev/null | awk '{print $1}')"
  [ -n "$_inner" ] || _inner="mtproxyl-panel"
  case "$_inner" in
    /*) : ;;
    *) _inner="/usr/local/bin/$_inner" ;;
  esac

  mkdir -p "$TEMP_DIR/backup"
  if docker cp "$CONTAINER:$_inner" "$TEMP_DIR/backup/mtproxyl-panel" 2>/dev/null; then
    $SUDO mkdir -p "$(dirname "$BACKUP")"
    $SUDO cp -f "$TEMP_DIR/backup/mtproxyl-panel" "$BACKUP"
    say "Прежний бинарник сохранён: $BACKUP"
  fi

  docker cp "$NEW_BINARY" "$CONTAINER:$_inner" || die "не удалось положить бинарник в контейнер"
  docker restart "$CONTAINER" >/dev/null || die "контейнер не перезапустился"
  sleep 3
  docker ps --format '{{.Names}}' | grep -qx "$CONTAINER" || die "контейнер не поднялся после перезапуска"

  say ""
  say "Готово, но прочитайте это до конца:"
  say "  Подмена внутри контейнера переживает docker restart и НЕ переживает"
  say "  пересоздание — docker compose up --build вернёт прежний образ."
  say "  Постоянный вариант: собрать образ из ветки форка."
  say ""
  say "    git clone -b tg-testing https://github.com/$REPO.git"
  say "    cd MTProxyL/mtproxyl-panel && docker compose up -d --build"
  say ""
  say "  И проверьте, что data_dir вынесен в том, иначе токен бота и id его"
  say "  сообщения исчезнут при первом же пересоздании контейнера:"
  say "    volumes:"
  say "      - mtproxyl-panel-data:/var/lib/mtproxyl-panel"
}

# ── откат ────────────────────────────────────────────────────────────────────
do_rollback() {
  [ -f "$BACKUP" ] || die "нет сохранённого бинарника ($BACKUP) — откатывать не к чему"
  case "$(detect_install)" in
    systemd)
      $SUDO install -m 0755 "$BACKUP" "$PANEL_BINARY"
      $SUDO systemctl restart "$SERVICE_NAME"
      say "Откат выполнен: $($SUDO "$PANEL_BINARY" version 2>/dev/null || echo '?')"
      ;;
    docker)
      docker cp "$BACKUP" "$CONTAINER:/usr/local/bin/mtproxyl-panel" || die "не удалось вернуть бинарник в контейнер"
      docker restart "$CONTAINER" >/dev/null
      say "Откат выполнен."
      ;;
    *) die "не нашёл установленную панель" ;;
  esac
}

main() {
  if [ "${1:-}" = "--rollback" ]; then
    do_rollback
    return
  fi

  _mode="$(detect_install)"
  case "$_mode" in
    systemd) fetch_binary; install_systemd ;;
    docker)  fetch_binary; install_docker ;;
    *) die "не нашёл ни службы $SERVICE_NAME, ни контейнера с панелью. Панель должна быть уже установлена — этот скрипт только обновляет её." ;;
  esac

  say ""
  say "Дальше: панель → «Доступность из России» → «Телеграм-бот»."
  say "Токен от @BotFather, затем напишите боту /start — он пришлёт ваш ID."
}

main "$@"
