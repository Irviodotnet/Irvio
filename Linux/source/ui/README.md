# HYDRA Linux UI

This Flutter application is the desktop client for HYDRA Local API v1.4.
It contains no VPN networking logic and communicates with `hydrad` only over
the Unix domain socket configured by `HYDRA_CONTROL_SOCKET` (default
`/run/irvio/control.sock`).

When negotiation falls back to Local API v1.0, status, Connect, Disconnect,
contribution capability detection, and status events remain available. Logs,
bootstrap onboarding, and Share Hydra are shown as unavailable. Profile
deletion is enabled only when negotiation reaches v1.2. Dynamic available
locations require v1.3; older daemons do not fall back to a static selectable
country list. Full daemon-owned Shutdown requires v1.4.

```bash
flutter pub get
flutter analyze
flutter test
flutter build linux --release
```

Run against a development socket with:

```bash
HYDRA_CONTROL_SOCKET=/tmp/hydra/control.sock flutter run -d linux
```

The alpha UI stores language, theme, notifications, and the optional Shutdown
confirmation preference locally. The close button and tray Shutdown action call
the daemon-owned Shutdown API; the UI does not kill the daemon or restart it.
On Linux,
Start at login manages only the HYDRA-owned
`~/.config/autostart/hydra.desktop` entry and launches the current GUI executable;
it never starts, stops, connects, or disconnects `hydrad`. Packaged releases must
revalidate the recorded executable path against the final install location (expected
to be `/usr/bin/hydra-ui`). Desktop notifications are emitted through the user's
normal notification session only for meaningful product-state transitions.

The generated Dart protobuf sources under
`lib/hydra/generated/irvio/local/v1/` correspond to
`../proto/irvio/local/v1/control.proto` and are committed so Linux release
builds do not require code generation.

See [`../docs/product/linux-gui-p2.2.md`](../docs/product/linux-gui-p2.2.md)
for the current product contract and lifecycle behavior.
