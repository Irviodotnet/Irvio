# Private IRVIO — native Cancel/reclose rendering fix

Mini-PC only, 2026-10-06. This resolves the earlier unclassified Xvfb black-frame observation for the reproduced sequence; it does not claim every compositor/driver combination is tested.

## Reproduction

Packaged UI from `5095442` ran with a real private combined host: one pinned upstream and one approved downstream. Clicking the window cross opened Shutdown confirmation. Cancel followed by another cross click after a requested 10ms delay produced a blank dark window, still blank after one second and a pointer hover. Another cross click restored the dialog; a plain Cancel followed by a one-second wait rendered normally. Screenshots were visually inspected; stderr contained renderer/plugin notices, not a crash stack.

The scenario's network teardown subsequently passed in `3c50212e`, 183.27s, host integrity true. That PASS concerns daemon-owned shutdown/cleanup, not the earlier failed visual behavior. The original immutable results remain unchanged.

`showDialog` resolves when the route is popped, before its dismissal animation finishes. The previous `finally` then cleared `_shutdownRequested`, allowing a second native show/focus and dialog request while the old overlay was fading out. A new widget regression failed before correction: native-show count became 2 where the in-flight dismissal should still fence it at 1.

## Correction

Use an explicit `DialogRoute`, preserving root navigation, captured themes and non-dismissible barrier. Await both its result and `route.completed` before releasing the shutdown guard or continuing confirmed shutdown. Repeated window/tray events during dismissal are ignored; after completion a fresh request works. No Core/network change, arbitrary sleep, new timer or renderer override was added.

Widget coverage includes rapid duplicate window/tray events during dismissal, ten repeated Cancel/reclose cycles, no persisted suppression preference on Cancel, intact root content, and one daemon Shutdown/UI exit on confirmation. Opening animations are settled before exercising dismissal, avoiding a test-clock case where a not-yet-animated route is removed synchronously.

## Corrected native result

Same packaged daemon: `0cd289e5bb042a648fbf182fb315e26ad35dd8b12c622c99ed18bc1d9ae9639a`. New local release UI executable: `a9806f3f3cba5bdc6499eed295eefde268f64f4a78f3b03d09fbe6799ae58770`; libapp.so: `5b478a1712d0562444beb6eef80340a2c4e8dc02917e512e4ca6f8e96c4980d6`. These are development build hashes, not a newly packaged archive.

In `85edd3b2`, real X input performed ten Cancel→cross sequences (requested 10ms delay plus xdotool's click processing), with 0.4s settling checks. All ten main-window captures matched the baseline's 20,088 bright pixels in the fixed content region; dialog captures had 15,749. The pixel oracle detects blank frames, not semantic correctness of arbitrary content. The final restored main screenshot was also visually inspected. Local screenshots and automation remain under `/home/ubuntu/irvio-private-artifacts/5095442/native-fixed-85edd3b2`; they contain lab identities and are not committed.

After the ten cycles (15.162s), a fresh dialog opened and confirmation triggered real daemon-owned Shutdown. Existing assertions verified upstream lease release, host WAN restoration, cleared combined intent, downstream fail-closed with zero scoped WAN leakage, and all network baselines. `/tmp/hydra-m4b-85edd3b2/results.json`: start `2026-10-06T23:00:17.140269358Z`, completion `23:01:12.39611603Z`, test PASS 55.19s, exit 0, host integrity true. Only the pre-existing management namespace remained; no test/GUI/daemon processes or hydrad cgroups remained.

Flutter analysis passed, four focused dialog tests passed, Linux release build passed, and the full Flutter suite passed 131 tests in 21 seconds. The installed mini-PC application and `5095442` archive still predate this UI correction; refresh the clean preview before final handoff. Native AppIndicator delivery remains covered separately; this new repetition used the actual window cross, not a mocked event.
