# IRVIO 1.0 — Linux and Windows

This release publishes the 2026-10-31 handoff: current cross-platform sources,
the latest prepared Ubuntu tarball and the latest prepared Windows MSI.
Product maturity remains Alpha/Preview as described in the README.

## Downloads

- Ubuntu 24.04 / 26.04, AMD64: `irvio-1.1.0-alpha.20261031.2-ubuntu-amd64.tar.gz`
- Windows 11, x64: `irvio-1.1.1019-preview-x64.msi`
- Each installer includes a companion `.sha256` checksum.

The release label is **1.0**. Existing installer filenames and embedded versions
are intentionally unchanged: these are the supplied tested artifacts, not
rebuilds or binary version edits. The Windows MSI is unsigned.

## Sources and provenance

- `Linux/source/` and `Windows/source/` contain the complete shared source tree
  from development commit `04e0a0cadaddf26f71266273c9cc30105fdce382`.
- The Linux tarball was built from that commit; see `Linux/installer/BUILD_INFO.json`.
- The Windows MSI was built on 2026-10-19, before the current source snapshot.
  The current Windows source includes later changes; it is not claimed to be
  the exact reproducible source revision of that MSI.
- Original handoff documentation is preserved in `HANDOFF.md`; its lowercase
  directory references correspond to the GitHub directories `Linux/` and `Windows/`.

Verify the repository snapshot from its root using `sha256sum -c SHA256SUMS`.
Verify downloaded installers using their companion checksums from the same directory.
SHA-256 checksums verify integrity, not publisher identity or code signing.

## По-русски

Релиз **1.0** объединяет актуальные исходники и последние готовые установщики
из handoff от 31 октября. Статус продукта остаётся Alpha/Preview.
Внутренние версии пакетов сохранены: Linux — `1.1.0-alpha.20261031.2`,
Windows — `1.1.1019-preview`. Установщики не пересобирались и не переименовывались
для имитации новой бинарной версии. MSI пока без цифровой подписи.

Исходники обеих платформ — актуальный снимок `04e0a0c`. Windows MSI собран
раньше этого снимка, 19 октября; не следует считать его точной сборкой приложенного HEAD.
Инструкции Linux находятся в `Linux/installer/install.md`, сборка MSI описана
в `Windows/source/packaging/windows/MSI.md`.
