# IRVIO — установка для Ubuntu

Этот архив предназначен для **Ubuntu Desktop 24.04 LTS, x86-64 / amd64**.
Это тестовая Alpha-сборка. Ubuntu 22.04, ARM64, другие дистрибутивы,
WSL и контейнеры этим пакетом не поддерживаются.

## 1. Распаковать и проверить

Откройте терминал в каталоге со скачанным архивом и его `.sha256`:

```bash
sha256sum -c irvio-1.1.0-alpha.20261005.1-ubuntu24.04-amd64.tar.gz.sha256
tar -xzf irvio-1.1.0-alpha.20261005.1-ubuntu24.04-amd64.tar.gz
cd irvio-1.1.0-alpha.20261005.1-ubuntu24.04-amd64
sha256sum --check --quiet SHA256SUMS
```

Скачивайте архив и контрольную сумму из доверенного источника. SHA-256
обнаруживает повреждение, но не заменяет подпись издателя: эта Alpha не подписана.

## 2. Установить

Перед установкой завершите старую IRVIO/HYDRA пунктом **Shutdown IRVIO** в трее
или крестиком окна с подтверждением. Не используйте `kill -9` и не удаляйте
firewall-правила, чтобы освободить место для установки.

Из терминала обычного пользователя рабочего стола:

```bash
sudo ./install.sh --user "$USER"
```

Установщик проверит систему, установит необходимые библиотеки через `apt`,
добавит указанного пользователя в группу `irvio`, установит приложение и
включит systemd socket activation. Во время `apt` нужен доступ к репозиториям
Ubuntu. Установщик **не подключает VPN** и не запускает GUI от root.

После успешной установки **выйдите из сеанса рабочего стола и войдите снова**:
это необходимо для применения новой группы доступа к Local API. Закрытия
одного терминала недостаточно. Затем найдите **IRVIO** в меню приложений либо:

```bash
irvio
```

Не запускайте `sudo irvio` и не открывайте UI с правами root.

## 3. Первое подключение

1. Откройте IRVIO. Демон запустится по обращению GUI и останется без VPN-подключения.
2. Импортируйте QR-код или текст `irvio-bootstrap:v1:...`, полученный отдельно.
3. Дождитесь списка доступных стран, выберите страну и нажмите CONNECT.
4. DISCONNECT вернёт обычное подключение к Интернету.
5. Крестик или **Shutdown IRVIO** в трее завершит сеть и приложение полностью.

Бинарники теперь называются `irvio` (интерфейс) и `irviod` (демон).
Службы называются `irviod.service`, `irvio.socket` и `irvio-boot-recover.service`.
Профиль находится в `/var/lib/irvio`, API — `/run/irvio/control.sock`, конфигурация —
`/etc/irvio`. Старые bootstrap-коды не принимаются; нужен `irvio-bootstrap:v1:`.

В архиве **нет** готового профиля, PeerID, ключей, bootstrap-ссылок или логов.
При первой установке идентичность создаётся на вашем устройстве. Существующий
профиль не удаляется. CONNECT включает и CLIENT, и участие в качестве EXIT.

## Что устанавливается

| Путь | Содержимое |
| --- | --- |
| `/opt/irvio/bin/irviod` | Сетевой демон |
| `/opt/irvio/ui/` | Полная release-сборка UI, библиотеки и assets |
| `/usr/local/bin/irvio` | Команда запуска UI |
| `/usr/local/share/applications/irvio.desktop` | Ярлык IRVIO |
| `/etc/systemd/system/irvio.socket` | Local API socket activation |
| `/etc/systemd/system/irviod.service` | Демон, запускаемый по запросу |
| `/etc/systemd/system/irvio-boot-recover.service` | Одноразовое восстановление после нового boot |
| `/var/lib/irvio/geo/country.mmdb` | DB-IP Country Lite, если базы ещё не было |
| `/var/lib/irvio/profile/` | Профиль, создаваемый демоном, не содержимое архива |

После загрузки ОС постоянно работающий idle-демон не нужен. Работает socket
activation, а boot-recovery oneshot завершает свою работу. GUI сам при входе
в систему не запускается, если пользователь не включил это в настройках.
Автоматическое VPN-подключение после нового boot отсутствует.

## Зависимости и DNS

Нужны systemd, доступный `/dev/net/tun` и **уже работающий systemd-resolved**
с `/usr/bin/resolvectl`. Установщик не заменяет ваш DNS-менеджер и не переписывает
`/etc/resolv.conf`. Если DNS в Ubuntu был специально перенастроен, установку
нужно остановить и сначала согласовать конфигурацию; не заменяйте DNS вслепую.

Библиотеки и инструменты, устанавливаемые через apt:

```text
libgtk-3-0t64 libayatana-appindicator3-1 libstdc++6 libgl1 libegl1 libgles2
libnotify-bin iproute2 nftables procps ca-certificates
```

Для проверки без установки:

```bash
./install.sh --check
```

Проверка read-only. Она может сообщить о недостающих библиотеках на чистой ОС;
обычная установка ставит их через apt. Если все зависимости уже установлены:

```bash
sudo ./install.sh --user "$USER" --no-deps
```

## Обновление и старая ручная установка HYDRA

Первая версия установщика намеренно **не перезаписывает** чужую или старую
ручную установку: `/opt/hydra/ui`, `/usr/local/bin/hydrad`, существующие службы
HYDRA, unit overrides и `/etc/irvio/config.json` требуют отдельной миграции.
Она не останавливает такие службы и не удаляет их автоматически.

Для обновления установки, сделанной именно этим tarball:

1. Выключите «Запускать при входе» в UI, если он включён.
2. Выполните Shutdown IRVIO.
3. Удалите установленную версию командой ниже (профиль сохранится).
4. Установите новый tarball. Не удаляйте `/var/lib/irvio`.

Прямое обновление поверх запущенного VPN, rollback и автоматическое обновление
в эту первую версию упаковки не входят.

## Удаление

Сначала при необходимости выключите автозапуск UI и выполните Shutdown IRVIO.
Затем:

```bash
sudo /opt/irvio/uninstall.sh
```

Либо используйте `sudo ./uninstall.sh` из распакованного архива.
Удаляются только проверенные файлы установки IRVIO, ярлык и три systemd units.
**Сохраняются** профиль/GeoIP в `/var/lib/irvio`, конфигурация `/etc/irvio`, группа
`irvio`, настройки UI и пользовательские autostart-файлы. Установленные apt-пакеты
не удаляются. Для уничтожения идентичности используйте **Delete profile** в UI
до удаления программы; purge-флага с автоматическим стиранием профиля нет.

При оставшемся `hydra0`, ConnectIntent или HYDRA nftables state удаление
отклоняется. Сначала восстановите работу демона и выполните штатный Shutdown.

## Диагностика

```bash
systemctl status irvio.socket irviod.service irvio-boot-recover.service
journalctl -u irviod.service -n 100 --no-pager
id -nG
```

Когда GUI закрыт и соединения нет, `irviod.service` может быть `inactive` —
это штатно. При открытии UI он запускается через `irvio.socket`. Группа `irvio`
должна быть видна в `id -nG` **в новом пользовательском сеансе**.

DB-IP Country Lite включена с атрибуцией: [IP Geolocation by DB-IP](https://db-ip.com/).
Источник и лицензия: [официальная страница DB-IP](https://db-ip.com/db/download/ip-to-country-lite),
см. `licenses/DB-IP-ATTRIBUTION.md`. Версия базы — сентябрь 2026; автоматического
обновления GeoIP нет. Детали сборки: `BUILD_INFO.json`. Результаты проверки
конкретной сборки публикуются отдельно от архива.
# Server без UI (Ubuntu 24.04 / 26.04 amd64)

После проверки и распаковки архива выполните из его каталога:

```bash
sudo ./install.sh --role server --bootstrap 'irvio-bootstrap:v1:<ПОЛНЫЙ_BUNDLE>'
```

Установщик ставит только серверные зависимости, daemon и GeoIP, создаёт `irvio-server.service`, включает автозапуск и до пяти минут проверяет текущий запуск: M11, STUN, Rendezvous, публикацию Exit, регистрации, порты и Safety Policy. `OK` означает успешную локальную проверку готовности, не внешний тест пользовательского трафика. Ошибка завершается ненулевым кодом и показывает команды диагностики. Повторная установка поверх существующей инсталляции не выполняется: профиль и ручные настройки сохраняются.

Постоянные внешние порты: **TCP/4000, UDP/3478, UDP/51820**. Порт ICE/AWG не меняется после рестарта; занятый порт вызывает ошибку, не выбор случайного порта. Активный UFW получает разрешения этих портов, Exit forwarding и внутреннего lease-control TCP/51822 на `hydra0` (этот TCP-порт не открывается на WAN). Выключенный firewall не включается. Неизвестные/custom firewall-конфигурации требуют ручного review вместо сброса правил. Внешний firewall провайдера настраивается у провайдера.

```bash
sudo /opt/irvio/scripts/check-server.sh
sudo systemctl status irvio-server.service --no-pager
sudo journalctl -u irvio-server.service -n 100 --no-pager
sudo systemctl stop irvio-server.service
sudo systemctl start irvio-server.service
```

Удаление: `sudo /opt/irvio/uninstall.sh`; сервер штатно останавливается, identity/GeoIP/bootstrap и firewall-разрешения сохраняются. M11 quorum не ослаблен; этот режим не создаёт одиночный genesis автоматически. GUI/socket activation и desktop DNS manager серверному установщику не нужны.
