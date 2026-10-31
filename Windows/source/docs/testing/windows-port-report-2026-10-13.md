# IRVIO для Windows — отчёт о выполненном переносе

Дата: 13 октября 2026 года. Рабочая ветка: `windows-port`.

## 1. Итог текущего этапа

Реализован работающий Windows desktop-порт с Global VPN, ролью CLIENT+EXIT,
Private VPN, Windows-службами, нативной защитой трафика, DNS/маршрутами, GUI и
самостоятельным development EXE-установщиком. Есть реальные межплатформенные
подключения и передача пользовательского трафика, а не только успешная компиляция.

Это **тестовая Windows-сборка, не полный production acceptance**. У серверного
режима реализованы preset и deployment, но успешная публичная M11-верификация и
выход второго клиента через Windows server на доступном стенде не доказаны.
Оставшиеся ограничения перечислены ниже, без объявления их исправленными.

По решению владельца проекта отдельный ноутбук не требуется. Текущие проверки
завершены; после выдачи этого отчёта большая задача Windows-порта ставится на паузу
до следующей команды. Новые hardware-сценарии, установочные кампании и длительный
release soak не запускаются.

## 2. Стенд и версии

- Две KVM/QEMU VM с Windows 11 Pro x64, семейство build 26300; независимый QEMU
  Guest Agent позволяет управлять гостем при заблокированной VPN-сети.
- Linux мини-ПК использовался как реальный клиент/Exit, партнёр Private VPN и
  исполнитель Linux-регрессий.
- Нативный Windows toolchain: Go 1.27.1, Flutter 3.47.4, Dart 3.13.3,
  Visual Studio C++/Windows SDK; для Go race — llvm-mingw/Clang.
- Последние runtime-проверки выполнялись на peer-VM с двумя разными виртуальными
  NIC, адресами, подсетями и шлюзами. Внешний канал хоста у них общий: это не
  эксперимент с двумя независимыми провайдерами.
- Прокси и сетевые настройки рабочей Ubuntu-станции не менялись. VPN-тесты на
  ней не запускались; она использовалась для исходников и сборочной оркестрации.

Свежий комплект собран из чистого Git-архива
`dfda72923e68741dd600cfdae4ce166e2b5ae186`. Более поздние коммиты документации
не меняют эту зафиксированную ревизию бинарного комплекта.

## 3. Что реализовано

| Подсистема | Работа для Windows |
| --- | --- |
| Runtime и службы | Demand-start daemon, SCM recovery после crash, штатный Shutdown, отдельная boot-recovery операция |
| Local API | Аутентифицированный Named Pipe и непривилегированный UI bridge; GUI не получает право менять службу |
| Identity/profile | Защищённые Windows ACL, persistent bootstrap/Private state, корректная обработка SQLite при Delete Profile |
| Сетевая платформа | Native IP Helper/NetIO, уведомления о сети, kernel route lookup, привязка source/interface и управление owned routes |
| Data Plane | Wintun и существующий AmneziaWG; generation/owner lifetime, без замены сетевого протокола |
| Защита и DNS | WFP ProtectedMode, scoped underlay permissions, журналируемые DNS/routes, fail-closed и восстановление после сбоев |
| EXIT | WinDivert/native packet I/O и per-lease NAT/forwarding; отделение собственного upstream от downstream WAN |
| Private VPN | Существующие приглашения, сохранённые подключения, exact-target и hosting через общий Core |
| GUI | Flutter Windows runner, bridge, tray, shutdown, single-instance, QR/file dialogs и Windows desktop-интеграция |
| Headless server | Windows SCM preset для DHT/STUN/Public Control/Rendezvous/EXIT; readiness не обходится |
| Packaging | Payload manifest, закреплённые зависимости и лицензии, EXE setup, update/rollback/uninstall, сохранение либо удаление профиля |

Основные Windows-адаптеры находятся в `internal/winroute`, `internal/windns`,
`internal/winfile`, `internal/wfp`, `internal/windivert`, а также Windows-файлах
`runtime`, `tunnel`, `socketmux`, `productintent`, `exitnat` и `cmd/irviod`.
UI bridge — `cmd/irvio-ui-bridge`; упаковка — `packaging/windows`.

## 4. Подтверждённые runtime-результаты

Результаты привязаны к конкретным сборкам в подробном журнале. Старые успешные
кампании нельзя автоматически считать прогоном новых байтов EXE.

### Global VPN и CLIENT+EXIT

- Выполнено 100/100 Global CLIENT+EXIT циклов на зафиксированной диагностической
  сборке: CONNECT, HTTPS, блокировка физически привязанной WAN-пробы, DISCONNECT,
  восстановление DNS/routes/adapters; процесс daemon сохранялся.
- Median подключения 10,637 с, p95 13,050 с; Disconnect median 1,669 с.
- В idle не накапливались TCP/UDP sockets; наблюдавшийся небольшой рост handles
  сохранён как observation, а не объявлен доказанным отсутствием любого leak.
- На установленном desktop проверен Disconnect при пяти активных downstream
  клиентах и собственном upstream: leases закрыты, вклад выключен, обычный WAN
  восстановлен, системная очистка подтверждена.

### Private VPN и GUI

- Проверены Linux↔Windows и Windows↔Windows Private-подключения с DNS/HTTPS.
- Кампания Private CLIENT+HOSTING: 10 warmup + 100 measured циклов; median CONNECT
  9,695 с, p95 18,642 с; median Disconnect 2,330 с. В idle — ноль активных sockets,
  DNS/routes/adapters возвращались к baseline.
- В настоящем standard-user fixture проверены запуск daemon из UI, импорт
  bootstrap, подключение, QR save/import по Unicode-пути, Delete Profile с
  реальной SQLite, повторный импорт и Shutdown.
- Убийство GUI не отключало daemon; повторное открытие GUI восстанавливало
  отображение сессии. Позднейшая installed-проверка использовала filtered token
  администратора — это отдельно отмечено, не подменяет standard-account опыт.

### Последние проверки network/crash recovery

На установленном daemon `bc09655`, SHA-256
`b83182f4d581830872a7c8d75b32ad4a86b124a5c5415b75024e962210c5dda8`:

| Сценарий | До CONNECTED | До завершённого HTTPS |
| --- | ---: | ---: |
| NIC 6 → NIC 11 | 11,972 с | 12,428 с |
| NIC 11 → NIC 6 | 12,495 с | 12,877 с |
| NIC 6 → NIC 11 с независимым packet capture | 19,152 с | 19,653 с |
| Принудительное завершение daemon, автоматический SCM restart | 10,562 с | 11,061 с |

В crash-тесте новый процесс наблюдался Running через 1,923 с; новый CONNECT
не отправлялся, country intent NL восстановился автоматически, boot/SCM остались
теми же. Некэшированный DNS после восстановления прошёл за 54 мс.

Независимые захваты на двух гостевых NIC подтвердили **ноль открытых пакетов
проверяемого IPv4 UDP-потока**: при миграции по 221 пробе с каждого источника,
при crash — по 153. Есть положительные контрольные пакеты до/после защиты.
Это sampled oracle с периодом около 100 мс, не доказательство для всех протоколов,
IPv6, начального CONNECT или intentional teardown. После тестов — точный baseline
сети, native cleanup и остановленные службы без оставшихся процессов-пробников.

Реальные cold reboot и hibernate также проверялись; BootType 2 сохранял сессию,
после resume возвращался трафик без нового CONNECT. Fast Startup этим не доказан.

## 5. Найденные и исправленные дефекты

Среди подтверждённых исправлений в ходе переноса:

- Windows-публикация identity: гонка читателя с ещё не закрытым DELETE-access
  handle после rename; ограниченное повторение sharing-violation без ослабления ACL.
- Delete Profile: корректный allowlist удаления SQLite-файлов с trusted inherited
  ACE под защищённым родителем; ключи и bootstrap не получили послаблений.
- Отмена underlay generation: независимая ограниченная очистка предыдущих grants
  вместо зависшего dirty/draining состояния после cancellation.
- Повторный `windowsClientRoutes.Close`: ошибка первого закрытия больше не
  навсегда кешируется; failed resources остаются во владении и допускают retry.
- Maintenance/обновление: порядок управления recovery-службой исправлен так,
  чтобы не лишать активный daemon возможности завершить owned teardown.
- Дополнительно исправлялись выявленные native I/O/owner lifetime и Windows
  installer ACL/strict-mode ошибки; подробные RED/GREEN и границы изменений —
  в `windows-port-validation.md`.

Последние cancellation/route-close исправления сначала воспроизведены focused
регрессиями на старом коде, затем прошли обычные и race/shuffle-проверки.
Wire/security проверки не ослаблялись ради успешного стенда.

## 6. Финальная сборка и текущая регрессия

Сборка daemon, `go vet ./...`, Flutter analyze, **160 UI tests**, Windows release
build, dependency staging и embedded bundle verification — PASS.
Независимо проверены 507 файлов payload, точный inventory и их size/SHA-256.

Финальная native whole-tree матрица на исходниках dfda729 завершена
13 октября в 11:32:36 UTC. Все семь стадий — PASS (exit 0):

| Стадия | Время, с | Результат |
| --- | ---: | --- |
| `go vet ./...` | 1,805 | PASS |
| `go test ./...` | 127,462 | PASS |
| `go test -shuffle=on -count=10 ./...` | 390,156 | PASS |
| `go test -race ./...` | 258,573 | PASS |
| `go test -race -shuffle=on -count=5 ./...` | 318,744 | PASS |
| Normal build | 3,143 | PASS |
| Trimpath build | 3,289 | PASS |

Использованы native Go 1.27.1/windows-amd64, CGO_ENABLED=1, llvm-mingw и
параллелизм сборки `-p=2`; точные команды с bounded timeout сохранены в harness.
Env-gated privileged suites этим запуском не включались. Их runtime evidence
перечислено отдельно, whole-tree PASS не заменяет такие проверки.

Первый bare `go test` был запущен без требуемых Windows loopback aliases и упал
на двух migration-тестах. Это ошибка подготовки harness, известная по предыдущей
проверке, а не новая ошибка продукта. Исходный FAIL сохранён. Используемый сейчас
committed fixture временно назначал 127.0.0.2/.3, не менял assertions и подтвердил
точное удаление своих aliases: `cleanup_delta=[]`. После прогона нет процессов
IRVIO/bridge/leakprobe; IRVIO Manual/stopped, IRVIO-Recovery Auto/stopped,
ExitCode=0 у обеих служб. Установщик для этих проверок не запускался.

Архив `native-full-evidence.tar` содержит 17 файлов с независимо проверенными
SHA-256; хеш архива:
`1d4d6750f5e8be2b69ac5115e30bdadce41fe2543fdc9399e3c8544f5a103e18`.
Normal/trimpath CGO-сборки регрессионного harness имеют SHA-256 соответственно
`54b0643db2dfa07b34083b63b1bb63fadc82c64cb6d1f5438024b7a060b0c0ed` и
`791aa7e18c8e74a9c84e26d048c80c2174063df9801e28264b263ca6ef3a4faf`.
Это не CGO=0 daemon из handoff-пакета; его хеш приведён ниже.

## 7. Где находится комплект

На рабочей станции:

```text
/home/ubuntu/Work/IRVIO_WINDOWS_BUILD/dfda729/
```

В VM **IRVIO-Win11-Peer**:

```text
C:\IRVIO-Lab\IRVIO-Windows-dfda729\
```

Основные файлы: `irvio-setup-development.exe`, `irvio-source-dfda729.tar`,
`irvio-windows-payload-dfda729.zip`, `SHA256SUMS`, `install.md`, `REPORT.md`
и `native-full-evidence.tar`.
EXE — самостоятельный development-установщик, **не MSI**. Go/Flutter SDK для
установки готового комплекта не нужны. Подробная сборка EXE/UI описана в install.md.

| Артефакт | SHA-256 |
| --- | --- |
| Setup EXE | `20533e0689f73c5576845ec1e8a8e10f35b921eaab681b43e790d37bd2d3ef02` |
| Daemon | `3a153faa75c30b4c3e8d0c51cddb9b7ad6ee95007373bb2e88e9894ff69451c9` |
| Payload ZIP | `78ae7e16ba6854dde432576cca7911a72c23bfa67625103e95b33a6ce96645a3` |
| Source archive | `a22246b1ea5bf6c134b68f7815df3998146f69771cca8758ac1881e048a34f15` |

Этот свежий EXE не устанавливался повторно после сборки: runtime-доказательства
выше относятся к явно указанным установленным бинарникам. Подписи официальных
драйверов проверены, но собственные EXE IRVIO пока не имеют release Authenticode.
Это не повод отключать Defender, Secure Boot или проверку подписей.

## 8. Открытые наблюдения и границы

1. В отдельных source-migration попытках был curl 6 / временный сбой DNS сразу
   после восстановления. Последующие успешные DNS-пробы не объясняют исходную
   причину; speculative cache-flush исправление не добавлено.
2. Редкий исторический WSAEACCES TCP/51822 не повторился в Global100, но причина
   не доказана. Не выдаём отсутствие повторения за исправление.
3. Private signaling чувствителен к рассинхронизации часов: контролируемое
   отставание Windows на 2 секунды воспроизводило отказ broker deadline.
   Синхронизация исправляла условия стенда, протокол не ослаблялся.
4. Windows headless server под NAT не прошёл публичную M11-reachability проверку.
   TCP/4000, UDP/3478,51820, preset, GeoIP и lifecycle реализованы/проверены,
   но полноценный публичный distributor/Exit ещё не принят.
5. Не выполнены Windows Home, физический ноутбук, реальный broadband↔mobile
   переход, S3/Modern Standby и полная DPI/multi-monitor матрица. По текущему
   решению пользователя новые стенды/сценарии не добавляются.
6. Два запроса hybrid shutdown дали cold BootType 0; их нельзя записывать как
   Fast Startup PASS. Полная IPv6/все-протокольная leak-матрица также не закрыта
   последними ограниченными UDP-захватами.
7. Новый многочасовой packaged release soak не запускался. Полный production
   verdict и release signing этим отчётом не заявляются.

## 9. Технический вывод

Перенос вышел за пределы прототипа: Windows реально работает как VPN-клиент и
Exit/Private-host, имеет GUI и устанавливаемый комплект, восстанавливается после
ряда реальных отказов и корректно очищает состояние в проверенных сценариях.
Текущий результат — **работающий development desktop-порт с документированными
ограничениями**, а не безусловный PASS всех пунктов исходного плана.

Подробные доказательства: `docs/testing/windows-port-validation.md`.
Краткая карта: `docs/testing/windows-port-current-status.md`.
Архивы стенда: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/`.
Секреты, профили и сырые captures не включены в публичный комплект.
