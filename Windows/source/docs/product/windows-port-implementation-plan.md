# IRVIO — план переноса актуальной версии на Windows

Дата: 2026-10-09. Статус: план, не реализация Windows-порта.

## 1. Зафиксированная исходная точка

- Ветка: `private-irvio`.
- Source baseline: `a181ac74e188bc2d3dea327c1a40f7f6ae6e4b1f` — `feat(private): consolidate invitation workflow and desktop UX`.
- В его историю входит `0aa274e`: Global VPN больше не управляет приватной сессией через кольцо HOSTING.
- Этот baseline включает Global VPN, Private VPN, автоматическую выдачу разрешения первому предъявителю одноразового приглашения, срок первичного использования один час, сохранённые подключения, отзыв доступа, статусы downstream-клиентов, актуальную навигацию и однокнопочную раздачу.
- Local API: 1.7. Сетевые Private/Global протоколы сохраняются.
- Go в `go.mod`: 1.27.1; AWG: `amneziawg-go/v3 v3.1.20260828`; Flutter/Dart брать из проверенного Linux SDK и lockfile, затем проверить совместимость Windows toolchain.
- Последние Linux проверки на мини-ПК: 156 Flutter tests PASS, analyze PASS, release UI build PASS; affected Go vet PASS; affected race suites PASS на повторном запуске. Первый запуск выявил нестабильное ожидание подписчика в существующем Local API тесте; затем 30 изолированных повторов PASS. Подробности: `docs/testing/private-irvio-v1-handoff.md`.
- Это source baseline, а не утверждение об обновлении GitHub release или tarball. Временные сборки, профили, логи, `key.txt`, лабораторные каталоги и копии документов в корне не включены.

Перед кодированием создать отдельную ветку `windows-port` от этого baseline либо от его потомка, содержащего только данный план. Не смешивать перенос с незапрошенными улучшениями Linux.

## 2. Что переносим и что не меняем

Первый пользовательский milestone: Windows desktop с Global VPN **CLIENT+EXIT**, Private VPN как клиент и как раздающий хост, текущий UI, приглашения, Delete Profile, Shutdown, автозапуск UI по настройке. Client-only прототип допустим как промежуточная проверка, но не считается готовым портом.

Полный parity milestone дополнительно включает headless `irviod.exe --role server --bootstrap 'irvio-bootstrap:v1:...'`: DHT, STUN, M11 verifier/reachability, Rendezvous и EXIT. Его готовность проверяется отдельно от desktop, не подразумевается автоматически.

Предлагаемая начальная матрица: Windows 11 x64 Home и Pro, с фиксацией конкретных поддерживаемых builds до реализации. ARM64, Windows 10 и Windows Server — дополнительные цели после отдельного решения и проверок, а не обещанная совместимость. Headless preset сначала можно проверять на выбранной Windows 11; поддержка Windows Server требует отдельного стенда.

Не меняем wire formats, PeerID, ServiceCID, M11 quorum, алгоритмы ExitSelector/ACTIVE/RESERVE/COLD, lease authority, ICE/AWG packet semantics и текущие совместимые строковые идентификаторы. Не обновляем зависимости массово ради порта.

Не входят в работу: доступ к LAN/RDP/VNC, телеметрия, новый approval workflow, новый протокол bootstrap, redesign Core, изменение MTU без измеренной причины. Private VPN остаётся доступом в Интернет с текущими запретами LAN/host.

## 3. Разведка по текущему коду

Проба на мини-ПК: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/irviod` завершилась ошибкой. Первые реальные блокеры: `newLinuxScanner/newLinuxSubscription`, `normalizePolicy`, `unix.Renameat2`, `unix.Flock`, `listenPhysical/setUDPMark`, `unix.EADDRINUSE/MSG_TRUNC`, `syscall.Stat_t`. Это начальный, не исчерпывающий compiler inventory; executable не получен.

В tracked tree на момент разведки: 560 Go-файлов, 57 файлов реализации с суффиксом `_linux.go`; `ui/windows/` отсутствует. Числа не являются оценкой объёма переписывания: часть общих файлов также содержит Linux-зависимости.

| Текущая область | Что обнаружено | Работа для Windows |
| --- | --- | --- |
| `cmd/irviod/main.go`, `product_linux.go`, `private_*_linux.go` | systemd environment, `syscall.Exec`, Linux composition root | Разделить CLI/общие intents и OS lifecycle; добавить SCM entry point |
| `internal/runtime/production_linux.go`, `m12_runtime_linux.go`, `m12_connector_linux.go`, `private_hosting_linux.go` | В Linux-файлах есть и общая orchestration, и OS-конструкторы | Вынести только платформонезависимую композицию, подключить backend factories |
| `internal/networkstate/monitor.go`, `netlink_linux.go`, `productruntime/underlay_linux.go`, `internal/linuxroute/` | netlink, Linux route tables/rules | Windows route/interface snapshot + уведомления, LUID/GUID identity |
| `internal/socketmux/`, `controlsocket/`, `controlbypass/`, `underlay/`, `exitselector/probe_*` | socket marks, cgroup, Linux sockets; non-Linux probes возвращают unavailable | Реальные Windows physical/tunnel binding и constrained bypass, не no-op заглушки |
| `internal/tunnel/linux.go`, `internal/awg/`, `awgbinding/` | NativeTun owner/borrower, custom mux bind | Wintun backend с сохранением AWG и ownership/generation semantics |
| `internal/killswitch/`, `safetypolicy/`, `exitfirewall/`, `ipforward/` | nftables, Linux forwarding/NAT, kernel counters | WFP protection и отдельно доказанный Windows Exit/NAT backend |
| `internal/runtime/tunnel_dns_linux.go` | resolvectl и Linux DNS journal | Windows DNS snapshot/apply/restore с учётом DHCP и нескольких интерфейсов |
| `internal/sessioncontrol/socket_linux.go` | привязка inner control к TUN; non-Linux stub ошибочный для реальной работы | Windows tunnel-bound direct renewal/RELEASE |
| `internal/localapi/server.go`, `auth_linux.go`, `client.go` | Unix listener, socket activation, UID/GID/SO_PEERCRED | Named pipe transport и Windows access control; общий framing/dispatch |
| `identity/`, `awgidentity/`, `protocol/incarnation.go`, `privateaccess/store_linux.go`, `productbootstrap/store.go`, `productprofile/profile.go` | POSIX locks, chmod/UID, atomic rename и защита от symlink | Windows ACL/locking/atomic replacement/reparse-point protections |
| `internal/productintent/store_linux.go` | `/run/irvio/session-state`, boot-local intent | Windows boot-scoped intent, crash adoption и teardown marker |
| `ui/lib/main.dart`, `irvio/linux_irvio_client.dart`, `app/linux_*` | Явный запрет не-Linux, Unix IPC, XDG, Linux notifications | Windows client transport, service launcher, tray/autostart/notifications; общие экраны оставить |
| `packaging/systemd/`, `packaging/ubuntu/` | Linux installer и services | Отдельный Windows installer/service registration/signing/upgrade |

## 4. Неприкосновенные контракты

1. UI работает без Administrator; установщик получает elevation один раз. Privileged daemon владеет сетью и ключами.
2. Новый boot: IDLE, нет auto-connect/auto-sharing, обычный WAN доступен. Start at login запускает только UI при включённой настройке.
3. Same-boot crash при активном ConnectIntent: защита остаётся, SCM перезапускает daemon, adoption и reconnect; при неуспехе NO_EXIT без WAN fallback.
4. DISCONNECT очищает intent до teardown, отзывает downstream leases/advertisement, восстанавливает DNS/routes и только затем снимает protection и подтверждает IDLE.
5. SHUTDOWN делает безопасный teardown и штатно останавливает службу. Никакого немедленного рестарта из-за фонового UI polling; UI сначала прекращает reconnect/polling. Незапланированная смерть GUI не разрывает VPN.
6. Global CONNECT сохраняет CLIENT+EXIT. Собственный traffic идёт через upstream, downstream — в physical WAN, не через upstream: anti-multihop обязателен.
7. Private targeting всегда exact PeerID, без переключения на публичный Exit. Membership переживает reconnect и restart; expiry приглашения ограничивает только первое использование.
8. Частичное создание ресурсов, route changes и ошибка promotion не оставляют handles, adapters, routes, DNS, filters или stale leases.
9. IPv6 либо поддержан end-to-end, либо явно закрыт под protection; не допускается bypass через IPv6 при работающем IPv4 VPN.
10. Linux installer/runtime и совместимость существующей сети не регрессируют.

## 5. Самые рискованные решения — проверить до большого рефакторинга

### 5.1 AWG и Wintun

Кандидат: существующий user-space AmneziaWG и Wintun, не замена AWG на WireGuardNT. В `go.mod` Wintun уже есть как indirect dependency, но это не доказательство работающего IRVIO backend. Проверить Windows implementation именно pinned AWG dependency, custom Bind, многопировость, borrowed device semantics и promotion. У Windows нет Linux `dup(fd)`-контракта, поэтому нужен явный wrapper владения без double-close и зависания reader/events.

Wintun предоставляет L3 adapter и официальный комплект подписанных DLL. Это не NAT, firewall или полноценный Exit: эти слои нужно реализовать отдельно. [Wintun](https://www.wintun.net/).

### 5.2 Fail-closed через WFP

Dynamic WFP objects удаляются при завершении owning session/process, поэтому использовать только их для kill switch нельзя. Static objects переживают процесс, но исчезают при остановке BFE; persistent objects имеют иной boot lifecycle. Выбор lifetime/provider/service association фиксируется отдельным ADR после эксперимента crash → adoption → reboot. Не считать static filters достаточным решением без тестов BFE и Fast Startup. [WFP object lifetimes](https://learn.microsoft.com/en-us/windows/win32/fwp/object-management).

Доказать policy на нужных Windows слоях для уже открытых потоков, новых потоков, IPv4/IPv6 и forwarded traffic; не ограничиться разрешением/запретом новых connect. Разрешения Control Plane не должны открывать WAN всем процессам или всем пакетам daemon. Защита от администратора, намеренно отключающего безопасность ОС, не является обещанием, но штатные lifecycle-события BFE нужно классифицировать и обработать.

### 5.3 Windows как Exit — основной feasibility gate

Проверить forwarding/NAT на целевых Home/Pro, вместе с Wintun, WFP, несколькими leases и CLIENT+EXIT. Нельзя просто включить ICS: это может менять общий host network state. WinNAT — кандидат, не заранее выбранный универсальный backend; Microsoft документирует ограничение одного internal NAT subnet prefix. Отдельно проверить конфликты с Hyper-V/WSL/Docker и невозможность перехватывать чужой NAT. [WinNAT setup/limitations](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/setup-nat-network).

Если native backend не обеспечивает контракт на обычном Windows desktop, сравнить альтернативу с user-space forwarding/NAT или специализированным драйвером. Это отдельное существенное решение с performance/security review, не скрытая замена Core. Не обещать Windows hosting до результата этого gate; client-only промежуточный build маркировать именно так.

### 5.4 On-demand daemon без systemd socket activation

Не предполагать, что подключение к Named Pipe само запускает SCM service. Предложение: установленная demand-start служба `IRVIO`, UI platform adapter запрашивает её запуск через SCM с узким SERVICE_START/QUERY_STATUS permission, затем подключается к pipe. Installer не даёт UI права менять service binary/configuration. Crash recovery actions и intentional stop различаются; service binary/path защищены ACL. [StartServiceW](https://learn.microsoft.com/en-us/windows/win32/api/winsvc/nf-winsvc-startservicew).

Это Windows-специфическое отклонение от Linux «UI знает только сокет», которое нужно явно согласовать при W00; постоянный privileged launcher не вводить молча. Новый boot cleanup — короткая startup recovery операция, не постоянно висящий idle daemon. Для boot-ID/Fast Startup/hibernate сначала доказать требуемую семантику; `%TEMP%` не аналог Linux `/run`.

## 6. Пошаговые задачи и acceptance

### W00 — стенд, scope и решения

- Создать Windows 11 x64 VM со snapshots/консольным доступом, независимым от IRVIO; затем физический Windows ПК для Wi-Fi/NAT/power tests. Мини-ПК оставить Linux-партнёром. Рабочую Ubuntu-станцию не использовать как VPN test host.
- Установить pinned Go и Flutter/Dart, Visual Studio C++ desktop toolchain и Windows SDK. Flutter Windows release собирать на Windows, не считать Linux cross-build эквивалентом. [Flutter Windows setup](https://docs.flutter.dev/platform-integration/windows/setup).
- Зафиксировать Windows editions/builds, Secure Boot, доступ к VM, CI runner, signing/distribution requirements. Без Windows стенда план и Linux cross-build возможны, Windows acceptance — нет.
- Принять ADR: service activation/ACL/user ownership, reboot semantics и Exit/NAT feasibility.
- Gate: сохранённые результаты Wintun packet I/O, WFP process-crash test, Windows Exit forwarding prototype; если NAT требует нового backend — отдельное решение до массового порта.

### W01 — минимальное разделение платформ, зелёный Linux baseline

- Из общих файлов вынести Unix syscalls в `_linux.go`; создать `_windows.go` counterparts. `cmd/irviod/main.go` разделить на общий parser и platform entry/restart/service helpers.
- В `runtime` вынести общие production/private lifecycle части из Linux-only files, используя уже имеющиеся `Protection`, `NetworkSource`, `NetworkReconciler`, `DeviceOwner`, `SessionInstaller` interfaces; добавлять только реально нужные узкие interfaces.
- Перенести `normalizePolicy` и прочую platform-neutral validation из Linux backend без изменения правил. В non-Linux stub build tags исключить Windows после появления настоящей реализации.
- Gate: compiler inventory без unresolved symbols; unsupported capability даёт явную ошибку, а не фальшивую готовность. Linux affected tests зелёные. Ни одного массового переименования Core ради косметики.

### W02 — безопасное хранилище и Local API

- Новые `internal/localapi/listener_windows.go`, `auth_windows.go`; общий dispatch/framing/limits оставить. Предлагаемый endpoint: `\\.\pipe\IRVIO.Control`.
- Явная DACL, только разрешённый local user/SID и service/admin; запрет remote pipe clients, anonymous, pipe squatting и чужого server instance. Проверка token клиента; тесты fast user switching и session isolation. Не открывать Local API по неаутентифицированному localhost TCP.
- Клиентские права не должны включать создание pipe instance; default pipe ACL недостаточен. [Named pipe security](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights).
- Платформенные store helpers для identity/AWG/bootstrap/privateaccess/incarnation/profile deletion: locks, атомарная запись, ACL, отказ от reparse-point/hardlink unsafe targets, recovery после torn write. Форматы состояния и private authorization не ослаблять.
- Пути через Windows Known Folders: executable под Program Files, service profile/GeoIP под ProgramData, UI preferences у пользователя. Не переносить Unix `/var/lib` буквально; DB-IP attribution сохраняется, MMDB не в Git.
- Gate: обычный user управляет своей установленной службой без UAC на CONNECT; другой user не читает ключи/приглашения и не удаляет профиль; старые API clients получают корректное version negotiation.

### W03 — network state, underlay и socket policy

- `internal/networkstate/netio_windows.go`, Windows productruntime resolver и route backend рядом с общими interfaces; Linux `internal/linuxroute/` не превращать в файлы с Windows-логикой.
- Native route/source selection через IP Helper; LUID/GUID вместо постоянного ifindex/name. `GetBestRoute2` и route/interface notifications — кандидаты, проверить применимость на целевых SDK. [GetBestRoute2](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getbestroute2).
- Реализовать physical binding для bootstrap/DHT/STUN/ICE/AWG, tunnel binding для sessioncontrol; заменить SO_MARK/cgroup semantics, а не игнорировать marks. DNS underlay не должен циклически зависеть от ещё не созданного tunnel DNS.
- Сохранить единый socketmux, классификацию STUN/ICE/AWG, family policy, обработку truncation/port conflict. Route notifications должны запускать существующий NetworkGeneration mechanism, включая новый STUN после NAT remap.
- Gate: Ethernet/Wi-Fi metrics, смена IP/default route, исчезновение интерфейса, несколько NIC, IPv6-only/dual-stack по заявленной матрице, route-to-own-IP не выбирается WAN; generation races bounded.

### W04 — Wintun + существующий AWG

- `internal/tunnel/windows.go`: create/adopt/configure/remove adapter, MTU и адреса, owner/borrower lifecycle. Secure DLL load из защищённого package path; версия/архитектура/signature фиксированы.
- Сохранить `awg.Transport`, custom bind, allowed IPs, peer key rotation, promotion и TunnelLease activation ordering.
- Gate: Windows ↔ Linux encrypted traffic, многопировый AWG, same-peer recovery, NAT remap и 100+ failed/successful device-generation cycles без роста handles/adapters. Default MTU не менять без причины.

### W05 — ProtectedMode, routes и DNS

- `killswitch/wfp_windows.go`, platform route backend, `runtime/tunnel_dns_windows.go`, Windows Safety Policy backend. Транзакционно устанавливать owner-scoped filters/routes; отделить traffic origin и directions.
- Сначала protection, затем маршруты/сессия; при ошибке остаётся fail-closed. Снимать protection только после безопасного teardown. Не разрешать user traffic через physical NIC при reconnect/NO_EXIT.
- DNS native APIs с baseline snapshot, DHCP/static state, suffix/search domains, несколькими интерфейсами и recovery journal. Предотвратить DNS leak через нетуннельный интерфейс; проверять обычный resolver и браузерный DoH traffic routing, не обещать управлять настройками всех приложений.
- Gate: независимый packet capture/probe oracle до/после CONNECT, physical route changes, DNS failures, daemon kill, BFE cases; после DISCONNECT host settings совпадают с baseline с учётом внешних изменений, чужие настройки не перетираются.

### W06 — Exit, dual-role и статистика

- Реализовать выбранный в W00 backend для `ipforward`, `exitfirewall`, routing/NAT и source validation; capabilities объявлять только после readiness всех ресурсов.
- Per-lease inner source ownership, запрет downstream → own host/LAN/другой downstream, запрет multihop, Safety Policy, management/control exceptions без wide bypass.
- Own upstream traffic и downstream forwarding одновременно: собственный публичный egress — Exit, forwarded egress — physical WAN хозяина. Host protection не должно блокировать допустимый downstream, а downstream не должно обходить собственный kill switch.
- `trafficstats` Windows backend: MY TRAFFIC upload+download, forwarded traffic и active CLIENTS по текущему договорённому смыслу; неизвестное не показывать как точный ноль.
- Gate: ≥5 downstream клиентов, часть за NAT; RELEASE при штатном отключении, expiry при потере связи, revoke, client disconnect не рвёт own upstream; конфликты с чужим NAT безопасно диагностируются, не «чинятся» удалением чужих правил.

### W07 — Private VPN parity

- Подключить ported store/runtime/IPC к существующим enrollment TLS, private locator DHT lookup, owner receipts, exact-target selector и lease authority.
- Сохранить последний UX: Share my Internet → hosting + часовая ссылка; первое использование atomically grant; клиент paste/QR → явный CONNECT; reconnect позже без нового approval; owner delete/revoke отзывает доступ.
- Gate: Linux host → Windows friend, Windows host → Linux friend, Windows ↔ Windows; разные initial Rendezvous, частичная смерть owner servers, addressless-provider resolution, expired/copied/used invites, simultaneous redeemers, cancel/redemption race, restart и Delete Profile.
- Global/Private экраны разделены; navigation не отправляет network commands. No public fallback при offline private host. Отдельно проверить combined own private upstream + hosting, если capability предлагается текущим runtime.

### W08 — Windows lifecycle службы

- Предлагаемый новый `internal/platformservice/` или узкие helpers в `cmd/irviod`; `golang.org/x/sys/windows/svc` рассмотреть без смены common runtime state machine.
- Demand start, bounded readiness, SCM crash restart, Shutdown exit success, GUI crash без Disconnect. SCM stop/shutdown проходит daemon-owned teardown; installation/update не завершают процесс до cleanup без явной аварийной политики.
- `productintent/store_windows.go`: boot-scoped durable intent для country/exact private target/hosting и teardown-pending. Проверенный boot discriminator, fencing и ACL; reboot/hibernate/Fast Startup не смешивать по wall-clock эвристике.
- Boot recovery чистит только прошлые owned остатки и завершает процесс; не восстанавливает прежний CONNECT после нового boot. Same-boot intentional Disconnect никогда не resurrects по crash recovery.
- Gate: реальные process kill и reboot, sleep/resume, hibernate/Fast Startup согласно выбранному контракту; 0 ordinary-WAN leaks при активном intent, после Shutdown нет IRVIO user processes и неожиданных restarts.

### W09 — Flutter Windows UX и интеграция

- Добавить `ui/windows/` runner; убрать Linux-only guard через platform factory в `main.dart`, а не удалением проверок без backends.
- Вынести общий Local API protobuf/event processing из `linux_irvio_client.dart`; `windows_irvio_client.dart` реализует pipe transport через проверенную библиотеку или тонкий native bridge. Не блокировать UI isolate ожиданием SCM/pipe/DNS.
- Общие controller/screens/widgets/l10n остаются. OS adapters: autostart, notifications, tray icon `.ico`, window close/shutdown, save QR/file picker, paths и installer shortcut.
- Один пользовательский UI-instance, отсутствие повторных служб при двойном запуске; скрытый/закрытый UI корректно взаимодействует с tray и Shutdown. Start at login выключен → UI не появляется после входа.
- Gate: все существующие widget tests + Windows integration tests; масштабирование 100/125/150/200%, разные мониторы, EN/RU/DE, длинные пути/Unicode user name, мгновенная CONNECTING-индикация и отсутствие зависаний.

### W10 — headless server preset

- Windows service install mode для `irviod.exe --role server --bootstrap <bundle>`; те же TCP/4000, UDP/3478 и UDP/51820 для server preset, не навязывать фиксированный порт desktop.
- WAN/source auto-detection, redundant bootstrap-derived STUN, M11 независимый quorum, GeoIP DB-IP Country Lite, DHT/Rendezvous/Exit readiness и Safety Policy. Genesis остаётся internal deployment mechanism.
- Gate: реальный второй клиент выходит через Windows server; restart, dynamic country readiness, verifier loss, bootstrap partial loss; public IPv4 и поддерживаемый NAT profile. До этого Windows server не объявлять поддержанным.

### W11 — installer, upgrade, signing и диагностика

- Новая `packaging/windows/`: один выбранный EXE/MSI installer, build manifest, dependencies/licenses, signed Wintun DLL, daemon/UI, GeoIP+attribution, shortcuts, SCM/ACL/firewall registration.
- Installer elevation, после установки обычный user сразу открывает UI без relogin. Не требовать отключить Defender/Firewall/Secure Boot или включить test-signing в релизе.
- Проверить необходимость отдельного driver signing pipeline только если W00 выбрал собственный драйвер; официальный Wintun не даёт автоматической подписи нашим EXE. Authenticode/signing identity — отдельный release input.
- Обновление сохраняет keys/profile/memberships, останавливает/запускает только owned service, rollback совместим с state schema. Uninstall предлагает сохранить или удалить profile и удаляет только собственные объекты; offline/partial-failure rollback тестируется.
- Event Log для service lifecycle и bounded rotating structured logs в защищённом ProgramData path; redaction secrets/invites, user-readable sanitized diagnostics. Bootstrap/QR не помещать в public logs.
- Gate: clean install, upgrade, repair, uninstall, interrupted install; SHA-256/signatures, provenance и лицензии; без лишних SDK/test files в пакете.

### W12 — интегрированная приёмка и release

- Native Windows normal/race tests где toolchain поддерживает; Go vet/test, shuffled repetitions, Flutter analyze/test/build. Cross-build не заменяет native проверки.
- Linux affected regression при каждом переносе common orchestration; критические NAT-remap, DP promotion, NativeTun, addressless-provider, COLD reacquisition, Safety Policy, M8/M9/M10/M11/M12 и Local API contracts сохраняются.
- 100 CONNECT/DISCONNECT cycles Global и Private; failed generations, downstream load и concurrent revoke; baseline/final handles, threads, RSS/working set, sockets, adapters, routes, filters, DNS и lease counts.
- Наблюдать минимум LAN broadband↔mobile NAT, EIM/restrictive NAT и предсказуемый отказ без relay там, где direct path невозможен. Не обещать NAT traversal во всех сетях.
- Mixed-version Linux↔Windows сеть, несколько раздельных bootstrap bundles, public/private discovery без hidden introducers. Протокол не требует rollout всех distributors ради ОС клиента.
- Отдельный packaged release soak после focused gates и разрешения на его запуск, автономный harness; сейчас новый soak не запускается.
- Итоговый `docs/testing/windows-port-validation.md`: отдельные verdict для CLIENT, EXIT/dual-role, Private hosting, service lifecycle, headless server, installer; unsupported items явно перечислены, не замаскированы общим PASS.

## 7. Порядок и зависимости

Критический путь: **W00 feasibility → W01 → W02/W03 → W04/W05 → W06 → W07/W08 → W09 → W10/W11 → W12**. UI scaffold можно подготовить раньше, но он не доказывает готовность сетевого порта. Каждый этап — небольшой проверяемый commit/series с Linux regression и явным rollback.

Первый практический результат после утверждения плана: Windows стенд + три прототипа (AWG/Wintun, crash-safe WFP, Exit/NAT). Именно после них оценивать календарный срок. Сейчас обещать «неделю» без проверки Windows Exit и kill switch было бы необоснованно.

## 8. Что нужно от владельца проекта перед реализацией

- Windows 11 VM/ПК с админ-доступом, snapshots/консолью, который можно терять по сети, перезагружать и очищать; не основная рабочая станция.
- Подтверждение начальной OS/edition/architecture matrix и demand-start SCM модели вместо Linux socket activation.
- Решение по Exit backend по итогам feasibility, если native Windows механизмы не обеспечивают текущий контракт.
- Signing/distribution input для готового установщика; отсутствие подписи не скрывать за инструкцией отключить защиту ОС.

До этих решений можно подготовить seams и тест-план, но нельзя достоверно объявить Windows production path работающим. Этот документ не меняет текущий Linux runtime и не разрешает новые серверные развёртывания.
