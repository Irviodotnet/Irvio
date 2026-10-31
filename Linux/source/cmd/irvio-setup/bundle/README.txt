IRVIO Windows installer build input directory.
packaging/windows/build-setup.ps1 temporarily creates payload.zip here.
Never commit payload.zip, private profiles, signing keys or build outputs.
Without a packaged archive and manifest digest the installer refuses to run.
