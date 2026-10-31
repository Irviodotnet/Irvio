# Private IRVIO — replay-fix local preview

Clean source `bbeb819246a2875be474505342297e40eeb8df30`, branch `private-irvio`, built on the mini-PC at `2026-10-06T22:07:40Z`. Go 1.27.1 / Flutter 3.47.4, offline caches. Includes revalidation of cached session-control lease success after revocation. No GitHub/public release or production distributor changes.

Checkout: `/home/ubuntu/irvio-private-preview-bbeb819`; artifacts: `/home/ubuntu/irvio-private-artifacts/bbeb819`. Archive: `irvio-private-preview-bbeb819246a2-ubuntu-amd64.tar.gz`.

| Artifact | SHA-256 |
| --- | --- |
| Archive | `ec4066fd25579e38db7f8c60aa4002e1aea35cab5efb16f28347f24c3ed2245a` |
| Trimpath daemon | `a6ebc8837e286ec101ca9f74e631940cfe36884649bb17c11d4464b1054b5fd0` |
| UI executable | `1c6c8e0b96e0d246313b88661763fe3777b6c370765af5f1bfdd067965c65aef` |
| UI libapp.so | `824124c91a17000f9634eaf64f77dd39181cd6596eee79dd861c7926dc7e9ed0` |
| Netns runner | `1b2f382351b8feaff6bac7a7ca3c6b35b480ce873ab030774d44ba57de04019b` |

Archive auditor: PASS, 567 files, safe paths/types/modes, exact checksum inventory and no profile/key/log files. Extracted `SHA256SUMS`: PASS. Source checkout remained clean after packaging. Existing tray plugin `app_indicator_new` deprecation warning remains; build succeeded.

Affected component suites on the mini-PC: runtime/productruntime/sessionestablish/tunnelsignal/exitselector all passed `-race -shuffle=on -count=3`. Renewal-specific and full session-control/policy/lifecycle results are in `private-irvio-renewal-replay-results.md`.

The same race/shuffle/count=3 gate passed on the clean checkout for `cmd/irviod`, `internal/localapi`, `internal/productprofile` and `internal/productintent` (13.842s / 4.515s / 1.033s / 1.040s). Inspected private lifecycle assertions include retained hosting being non-idle, all three intent combinations, full leave clearing persisted intent, and profile-reset preparation clearing hosting recovery intent. These are component tests with protection hooks, not a new active-lease profile-deletion dataplane proof.

The isolated privileged campaign completed with all four cases PASS: `TestPrivateRuntimeEnrollmentTrafficAndRevocation` twice (21.23s / 19.89s), and `TestPrivateRuntimeDisconnectRelease` twice (65.43s / 65.53s). Artifact `/tmp/hydra-m4b-785d383f/results.json` records start `2026-10-06T22:08:27.13715134Z`, completion `22:11:19.265003499Z`, exit 0 and host integrity true. Real HTTP/UDP/DNS/ICMP, exact target, membership revocation, scoped WAN checks, repeated disconnect/RELEASE and cleanup assertions passed using the extracted daemon. Only pre-existing `hydra-mgmt` namespace remained; no runner or extracted daemon processes remained. The lab uses the existing isolated DNS launcher, not actual host resolver configuration.

The installed mini-PC application remains preview `01cfb88`; building and extracting this archive does not update it. Final installed acceptance and full requirement audit remain open.
