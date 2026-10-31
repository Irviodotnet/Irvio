# Private IRVIO — shutdown dialog regression

## Native follow-up

`private-irvio-native-dialog-fix.md` supersedes the unresolved black-frame observation below: reproduced on the packaged native UI, localized to releasing the close guard before route dismissal completed, corrected and verified with ten native rapid Cancel/reclose cycles plus combined shutdown. Full Flutter suite now passes 131 tests. Historical widget scheduling findings below retain their original scope.

## Follow-up: real controller fixture resolved

On the mini-PC after `6ed9f79`, the real-controller fixture reproduced the earlier failure: one shutdown API call, zero UI exits immediately after `pumpAndSettle()`. The production controller awaited broadcast subscription cancellation. Interleaving bounded `tester.runAsync` turns with widget pumps allowed that cancellation and the remaining cleanup to finish without any production change. This establishes a widget scheduling limitation for this fixture, not a production shutdown deadlock.

Added window-close and tray-event variants using the real `IrvioAppController` and real confirmation dialog with the fake Local API; only native focus and process exit remain stubbed. Both require exactly one shutdown call, one exit and no API error. Five complete repetitions of the three-test file passed (15 tests including rapid cancel/reclose); targeted Flutter analysis passed. The wait is bounded to twenty 10ms real turns, rather than an unbounded retry.

This resolves the combined widget fixture question below. It does **not** exercise native AppIndicator menu delivery or resolve the separate Xvfb black-frame observation. No production UI behavior changed.

Full mini-PC `flutter test --no-pub`: all 130 tests passed in 18 seconds, including private onboarding/controller guards, three languages, profile and bootstrap UI, public HOME and 500 bounded Local API socket lifecycles. Canonical and tested `linux_shutdown_dialog_test.dart` SHA-256 both equal `629f4f86613d846ccc96228750714506274f72920304001da048847f8522353f`.

## Earlier investigation

Mini-PC validation, 2026-10-06, based on `cf3ddbe`. No production code changed.

`ui/test/linux_shutdown_dialog_test.dart` exercises the real desktop dialog and window/tray event dispatch, with explicit stubs for native focus, process exit and the controller shutdown operation. Ten Cancel/reclose cycles deliberately reopen before the previous closing animation has settled. Concurrent window/tray requests must produce one dialog. Cancel must neither request shutdown nor persist the checked suppression preference. Final confirmation must request shutdown and UI exit exactly once.

Mini-PC commands:

```text
flutter test --no-pub test/linux_shutdown_dialog_test.dart test/irvio_app_controller_test.dart
25 tests passed
flutter analyze --no-pub test/linux_shutdown_dialog_test.dart
No issues found
```

This is widget/event-dispatch evidence, not native tray/AppIndicator or compositor evidence. The black-frame observation from the earlier Xvfb run remains unclassified. The existing controller tests independently exercise shutdown through the fake Local API.

Follow-up temporary diagnostics localized the combined widget fixture's wait to status-event subscription cancellation, after log subscription cleanup and before UI exit. The cancellation completed during test failure/teardown. Using separate fake status/log clients did not resolve it; moving initialization into `runAsync` also failed to complete. Temporary diagnostic edits and the unsuccessful experimental fixture were removed. Native stream cancellation remains a separate verification item; this does not establish a production deadlock.

An initial combined fixture using a started real controller and FakeIrvioClient reached one shutdown API call but did not reach the overridden UI exit within widget pumping (nor a mixed real/fake asynchronous wait). Its cause is unresolved; it is not classified as a product bug or dismissed as a proven harness artifact. The narrower dialog fixture above does not close that integration question. Native confirmation previously passed in two separate real-runtime experiments; retain both evidence scopes.

At the end of this check, `irvio-private-lifecycle-01cfb88-r1.service` remained active with MainPID 284295. The queued multi-client service remained active with MainPID 285928; no terminal result is claimed for either campaign.
