# Private IRVIO — public selection isolation

Source review and mini-PC regressions, 2026-10-06, baseline `5afa623` plus the country-metadata regression described below. No production changes.

| Boundary | Enforcement | Evidence |
| --- | --- | --- |
| Signed descriptor | `protocol/announcement.go` rejects simultaneous CapExit/CapPrivateExit | `TestPrivateRegistryIsolationAndReceipts` also attempts to sign mixed capabilities |
| Public DHT advertisement | `discovery.Node.ActivateExit` rejects active private hosting under provider lock; `SetPrivateExitReady` rejects public publication and does not start public provider loop | `TestPrivateExitReadinessIsolatedFromPublicPublication`, `TestPrivateExitConcurrentPublicActivation` |
| Runtime registration choice | `m12Runtime.startRegistrationManager` requires private endpoint presence to match `PrivateExitActive`; private start requires its endpoint | Source inspection; runtime readiness/withdrawal tests remain separate evidence |
| Public registration ingress | `Registry.register` selects strict public/private descriptor verification and validates locator domain | `TestPrivateRegistryIsolationAndReceipts` rejects both directions of registry crossing |
| Country and candidate RPC | Private registry exposes no countries and rejects public sampling | Same registry test; real libp2p `TestPrivateServiceRegistrationLookupAndNoPublicFallback` |
| Candidate wire conversion | Public encoder rejects private capability; public decoder requires public descriptor and receipt domains | Registry test checks both encoder and decoder rejection |
| Selector | Private target requires exact PeerID, private capability and brokered transport; public target rejects private capability | `TestPrivateTargetExactIdentityAndPublicIsolation` |

The selector regression now explicitly changes target candidate country metadata from DE to NL to empty while advancing its descriptor sequence. Exactly the pinned identity remains ACTIVE, unrelated candidates never reach probing, and neither RESERVE nor COLD is populated. This is a selector-level proof, not a real geographic migration experiment. Ordinary private hosting publishes an empty country.

Mini-PC commands/results:

```text
go test -p 1 -race -shuffle=on -count=10 \
  -run 'TestPrivateRegistryIsolationAndReceipts|TestPrivateServiceRegistrationLookupAndNoPublicFallback|TestPrivateExit|TestPrivateTarget' \
  ./internal/rendezvous ./internal/discovery ./internal/exitselector
rendezvous PASS 3.111s; discovery PASS 2.592s; exitselector PASS 1.127s

go test -p 1 -race -shuffle=on -count=10 ./internal/exitselector
PASS 2.900s (including the new country-metadata cases)
```

Unsupported private protocol was exercised on a public-only service using the current library, not by deploying an old release. Public discovery isolation does not mean an authorized or malicious distributor cannot observe the private descriptor/locator it handles. It also does not imply DHT observers cannot retain prior routing hints. These remain explicit privacy limitations, not anonymity claims.
